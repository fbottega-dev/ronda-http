// Command package builds portable, dependency-free archives from the repository
// root. Run: go run ./scripts/package --version 1.0.0 --output dist
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9][A-Za-z0-9.-]*)?$`)

type platform struct{ os, arch string }

var platforms = []platform{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
}

type entry struct {
	name string
	data []byte
	mode os.FileMode
}

type builder func(root, destination, version string, target platform) error

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Erro:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("package", flag.ContinueOnError)
	flags.SetOutput(out)
	version := flags.String("version", "", "versão dos executáveis, por exemplo 1.0.0")
	output := flags.String("output", "dist", "diretório novo ou vazio para os pacotes")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("argumento inesperado; use --version e --output")
	}
	return createPackages(".", *output, *version, build, out)
}

func createPackages(root, output, version string, compile builder, out io.Writer) error {
	if !versionPattern.MatchString(version) {
		return errors.New("version deve seguir MAJOR.MINOR.PATCH, com sufixo opcional como -rc.1")
	}
	assets, err := readAssets(root)
	if err != nil {
		return err
	}
	output, err = prepareOutput(output)
	if err != nil {
		return err
	}
	// Binaries live in a uniquely created temporary directory. Only that
	// directory is removed; existing output files are never replaced or deleted.
	temporary, err := os.MkdirTemp("", "ronda-package-")
	if err != nil {
		return fmt.Errorf("criar diretório temporário: %w", err)
	}
	defer os.RemoveAll(temporary)
	var checksums strings.Builder
	for _, target := range platforms {
		binaryName, extension, mode := "ronda", ".tar.gz", os.FileMode(0755)
		if target.os == "windows" {
			binaryName, extension, mode = "ronda.exe", ".zip", 0644
		}
		binaryPath := filepath.Join(temporary, target.os+"-"+target.arch+"-"+binaryName)
		if err := compile(root, binaryPath, version, target); err != nil {
			return fmt.Errorf("compilar %s/%s: %w", target.os, target.arch, err)
		}
		binary, err := os.ReadFile(binaryPath)
		if err != nil {
			return fmt.Errorf("ler executável: %w", err)
		}
		entries := append([]entry{{name: binaryName, data: binary, mode: mode}}, assets...)
		name := fmt.Sprintf("ronda-http_%s_%s_%s%s", version, target.os, target.arch, extension)
		sum, err := writeArchive(filepath.Join(output, name), entries, target.os == "windows")
		if err != nil {
			return fmt.Errorf("gravar %s: %w", name, err)
		}
		fmt.Fprintf(&checksums, "%s  %s\n", sum, name)
		if _, err := fmt.Fprintln(out, name); err != nil {
			return fmt.Errorf("escrever progresso: %w", err)
		}
	}
	file, err := os.OpenFile(filepath.Join(output, "checksums.txt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("criar checksums.txt: %w", err)
	}
	_, writeErr := io.WriteString(file, checksums.String())
	return errors.Join(writeErr, file.Close())
}

func build(root, destination, version string, target platform) error {
	command := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+version, "-o", destination, "./cmd/ronda")
	command.Dir = root
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.os, "GOARCH="+target.arch)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func prepareOutput(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("output deve indicar um diretório")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(abs, 0755); err != nil {
			return "", err
		}
		info, err = os.Lstat(abs)
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("output deve ser um diretório regular, sem link simbólico")
	}
	files, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}
	if len(files) != 0 {
		return "", errors.New("output já contém arquivos; escolha um diretório novo ou vazio")
	}
	return abs, nil
}

func readAssets(root string) ([]entry, error) {
	names := []string{"README.md", "docs/ESTUDO.md", "docs/EVOLUCAO.md"}
	// Packaging must preserve the project's licensing choice. Include an
	// existing license, but do not require or invent one for a project without it.
	if _, err := os.Lstat(filepath.Join(root, "LICENSE")); err == nil {
		names = append(names, "LICENSE")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("verificar LICENSE: %w", err)
	}
	fixedAssetCount := len(names)
	examples, err := os.ReadDir(filepath.Join(root, "examples"))
	if err != nil {
		return nil, fmt.Errorf("ler exemplos; execute na raiz do repositório: %w", err)
	}
	for _, example := range examples {
		if !example.IsDir() && strings.HasSuffix(example.Name(), ".json") {
			names = append(names, "examples/"+example.Name())
		}
	}
	if len(names) == fixedAssetCount {
		return nil, errors.New("nenhum exemplo JSON encontrado")
	}
	assets := make([]entry, 0, len(names))
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("arquivo ausente ou não regular: %s", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("ler %s: %w", name, err)
		}
		assets = append(assets, entry{name: name, data: data, mode: 0644})
	}
	return assets, nil
}

func writeArchive(path string, entries []entry, windows bool) (string, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	if windows {
		err = writeZip(writer, entries)
	} else {
		err = writeTarGzip(writer, entries)
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// Fixed timestamps avoid differences caused solely by package creation time.
var archiveTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

func writeZip(out io.Writer, entries []entry) error {
	archive := zip.NewWriter(out)
	for _, file := range entries {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate, Modified: archiveTime}
		header.SetMode(file.mode)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return errors.Join(err, archive.Close())
		}
		if _, err := writer.Write(file.data); err != nil {
			return errors.Join(err, archive.Close())
		}
	}
	return archive.Close()
}

func writeTarGzip(out io.Writer, entries []entry) error {
	compressed := gzip.NewWriter(out)
	archive := tar.NewWriter(compressed)
	for _, file := range entries {
		header := &tar.Header{Name: file.name, Mode: int64(file.mode), Size: int64(len(file.data)), ModTime: archiveTime, Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			return errors.Join(err, archive.Close(), compressed.Close())
		}
		if _, err := archive.Write(file.data); err != nil {
			return errors.Join(err, archive.Close(), compressed.Close())
		}
	}
	return errors.Join(archive.Close(), compressed.Close())
}
