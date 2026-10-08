// Package history stores opt-in, immutable snapshots of safe check reports.
package history

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fbottega-dev/ronda-http/internal/checker"
	"github.com/fbottega-dev/ronda-http/internal/report"
)

const timestampFormat = "20060102T150405.000000000Z"

var snapshotID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{12}$`)

// Entry is the metadata shown when listing saved reports. An ID can be passed
// directly to Load; it never contains a directory component or extension.
type Entry struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	Passed    int       `json:"passed"`
	Failed    int       `json:"failed"`
	Canceled  bool      `json:"canceled"`
}

// Save persists a validated report without replacing any existing snapshot.
// IDs combine the report's UTC start time with six cryptographically random
// bytes. The fully written and synced temporary file is published by a hard
// link, whose no-replace semantics also hold across concurrent processes.
// A filesystem supporting hard links is required (for example, NTFS or ext4).
// No automatic deletion or retention policy is applied.
func Save(dir string, r checker.Report) (string, error) {
	if err := report.Validate(r); err != nil {
		return "", fmt.Errorf("relatório inválido para o histórico: %w", err)
	}
	// Keep the on-disk timestamp and its fixed-width, sortable ID in agreement.
	r.StartedAt = r.StartedAt.UTC()
	if r.StartedAt.Year() < 1 || r.StartedAt.Year() > 9999 {
		return "", errors.New("data do relatório inválida para o histórico")
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", errors.New("não foi possível codificar o histórico")
	}
	data = append(data, '\n')
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errors.New("não foi possível criar a pasta do histórico")
	}
	temporary, err := os.CreateTemp(dir, ".ronda-*.tmp")
	if err != nil {
		return "", errors.New("não foi possível preparar o arquivo do histórico")
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return "", errors.New("não foi possível gravar o histórico")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", errors.New("não foi possível sincronizar o histórico")
	}
	if err := temporary.Close(); err != nil {
		return "", errors.New("não foi possível fechar o arquivo do histórico")
	}
	return publish(dir, temporaryName, r.StartedAt, randomSuffix)
}

// publish accepts a suffix source so collision handling can be exercised
// without replacing the process-wide cryptographic random source in tests.
func publish(dir, temporaryName string, startedAt time.Time, suffix func() (string, error)) (string, error) {
	for range 10 {
		random, err := suffix()
		if err != nil {
			return "", errors.New("não foi possível gerar o identificador do histórico")
		}
		id := startedAt.Format(timestampFormat) + "-" + random
		if err := os.Link(temporaryName, filepath.Join(dir, id+".json")); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", errors.New("não foi possível publicar o histórico; a pasta deve permitir hard links")
		}
		return id, nil
	}
	return "", errors.New("não foi possível gerar um identificador único para o histórico")
}

func randomSuffix() (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

// Load reads exactly one snapshot identified by an ID returned by Save or List.
// Paths, file extensions, symbolic links and unsupported reports are rejected.
// Errors never include file contents or local paths.
func Load(dir, id string) (checker.Report, error) {
	startedAt, err := parseID(id)
	if err != nil {
		return checker.Report{}, err
	}
	name := filepath.Join(dir, id+".json")
	info, err := os.Lstat(name)
	if err != nil {
		return checker.Report{}, errors.New("não foi possível localizar a execução no histórico")
	}
	if !info.Mode().IsRegular() {
		return checker.Report{}, errors.New("a execução do histórico deve ser um arquivo regular")
	}
	file, err := os.Open(name)
	if err != nil {
		return checker.Report{}, errors.New("não foi possível abrir a execução do histórico")
	}
	defer file.Close()
	r, err := report.Decode(file)
	if err != nil {
		return checker.Report{}, fmt.Errorf("execução do histórico inválida: %w", err)
	}
	if !r.StartedAt.Equal(startedAt) {
		return checker.Report{}, errors.New("data da execução do histórico difere do identificador")
	}
	return r, nil
}

// List returns at most limit snapshots, newest first by UTC start time. The
// random suffix breaks ties deterministically, not by order of creation.
// Only the selected snapshots are decoded. Unrelated files, symbolic links and
// unfinished temporary files are ignored. A missing directory is an empty list.
func List(dir string, limit int) ([]Entry, error) {
	if limit < 1 || limit > 200 {
		return nil, errors.New("o limite do histórico deve estar entre 1 e 200")
	}
	directory, err := os.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, errors.New("não foi possível ler a pasta do histórico")
	}
	defer directory.Close()
	// On Windows, ReadDir on a regular file may report ErrNotExist. Check the
	// opened handle first so an invalid path is never treated as empty history.
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		return nil, errors.New("o caminho do histórico deve ser uma pasta")
	}
	files, err := directory.ReadDir(-1)
	if err != nil {
		return nil, errors.New("não foi possível ler a pasta do histórico")
	}
	ids := make([]string, 0, len(files))
	for _, file := range files {
		if !file.Type().IsRegular() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".json")
		if _, err := parseID(id); err == nil {
			ids = append(ids, id)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	ids = ids[:min(limit, len(ids))]
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		r, err := Load(dir, id)
		if err != nil {
			return nil, fmt.Errorf("histórico contém uma execução ilegível (%s): %w", id, err)
		}
		entries = append(entries, Entry{
			ID: id, StartedAt: r.StartedAt, Passed: r.Passed, Failed: r.Failed, Canceled: r.Canceled,
		})
	}
	return entries, nil
}

func parseID(id string) (time.Time, error) {
	if !snapshotID.MatchString(id) {
		return time.Time{}, errors.New("identificador de histórico inválido")
	}
	startedAt, err := time.Parse(timestampFormat, id[:len(timestampFormat)])
	if err != nil || startedAt.Year() < 1 {
		return time.Time{}, errors.New("identificador de histórico inválido")
	}
	return startedAt, nil
}
