package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "examples"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"LICENSE": "MIT license", "README.md": "Guide", "docs/ESTUDO.md": "Study guide", "docs/EVOLUCAO.md": "Changelog", "examples/demo.json": `{"version":1}`, "examples/auth.json": `{"version":1}`, "examples/ignored.txt": "not JSON"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCreatePackagesContentsChecksumsAndPermissions(t *testing.T) {
	root := fixture(t)
	output := filepath.Join(t.TempDir(), "dist")
	var builds []platform
	compile := func(source, destination, version string, target platform) error {
		if source != root || version != "1.0.0" {
			t.Fatalf("build recebeu parâmetros incorretos: %s %s", source, version)
		}
		builds = append(builds, target)
		return os.WriteFile(destination, []byte("binary "+target.os+" "+target.arch), 0755)
	}
	var log bytes.Buffer
	if err := createPackages(root, output, "1.0.0", compile, &log); err != nil {
		t.Fatal(err)
	}
	if len(builds) != 6 {
		t.Fatalf("builds=%d, esperado 6", len(builds))
	}
	checksums, err := os.ReadFile(filepath.Join(output, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(checksums)), "\n")
	if len(lines) != 6 {
		t.Fatalf("checksums: %s", checksums)
	}
	for index, target := range builds {
		fields := strings.Fields(lines[index])
		if len(fields) != 2 || strings.ContainsAny(fields[1], "/\\") {
			t.Fatalf("checksum inválido: %s", lines[index])
		}
		binaryName, extension := "ronda", ".tar.gz"
		if target.os == "windows" {
			binaryName, extension = "ronda.exe", ".zip"
		}
		wantName := fmt.Sprintf("ronda-http_1.0.0_%s_%s%s", target.os, target.arch, extension)
		if fields[1] != wantName {
			t.Fatalf("pacote=%q, esperado %q", fields[1], wantName)
		}
		data, err := os.ReadFile(filepath.Join(output, fields[1]))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != fields[0] {
			t.Fatalf("SHA256 não confere: %s", fields[1])
		}
		files := readArchive(t, data, target.os == "windows")
		if len(files) != 7 {
			t.Fatalf("quantidade de arquivos: %d", len(files))
		}
		for _, name := range []string{binaryName, "LICENSE", "README.md", "docs/ESTUDO.md", "docs/EVOLUCAO.md", "examples/auth.json", "examples/demo.json"} {
			if _, ok := files[name]; !ok {
				t.Errorf("arquivo ausente: %s", name)
			}
		}
		binary := files[binaryName]
		if string(binary.data) != "binary "+target.os+" "+target.arch {
			t.Fatal("executável do alvo incorreto")
		}
		if target.os != "windows" && binary.mode.Perm() != 0755 {
			t.Errorf("permissão Unix: %o", binary.mode.Perm())
		}
		if string(files["LICENSE"].data) != "MIT license" || files["README.md"].mode.Perm() != 0644 {
			t.Fatal("metadados do pacote incorretos")
		}
	}
	if err := createPackages(root, output, "1.0.0", compile, io.Discard); err == nil {
		t.Fatal("diretório não vazio foi reutilizado")
	}
	if len(builds) != 6 {
		t.Fatal("iniciou build antes de rejeitar saída existente")
	}
}

func readArchive(t *testing.T, data []byte, windows bool) map[string]entry {
	t.Helper()
	result := make(map[string]entry)
	if windows {
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range reader.File {
			stream, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			content, readErr := io.ReadAll(stream)
			if err := errors.Join(readErr, stream.Close()); err != nil {
				t.Fatal(err)
			}
			result[file.Name] = entry{name: file.Name, data: content, mode: file.Mode()}
		}
		return result
	}
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		result[header.Name] = entry{name: header.Name, data: content, mode: header.FileInfo().Mode()}
	}
	return result
}

func TestInvalidVersionAndExistingOutputDoNotBuildOrOverwrite(t *testing.T) {
	root := fixture(t)
	compile := func(_, _, _ string, _ platform) error {
		t.Fatal("build não deveria ser chamado")
		return nil
	}
	for _, version := range []string{"", "dev", "v1.0.0", "1.0", "1.0.0/../../secret", "1.0.0 -X main.version=other", "1.0.0\n"} {
		output := filepath.Join(t.TempDir(), "dist")
		if err := createPackages(root, output, version, compile, io.Discard); err == nil {
			t.Errorf("versão inválida aceita: %q", version)
		}
		if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("versão inválida criou diretório de saída")
		}
	}
	output := t.TempDir()
	marker := filepath.Join(output, "existing.txt")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := createPackages(root, output, "1.0.0", compile, io.Discard); err == nil {
		t.Fatal("diretório existente não vazio aceito")
	}
	if _, err := prepareOutput(marker); err == nil {
		t.Fatal("arquivo aceito como diretório")
	}
	if _, err := writeArchive(marker, []entry{{name: "ronda", data: []byte("new"), mode: 0755}}, false); err == nil {
		t.Fatal("arquivo existente sobrescrito")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("arquivo existente alterado: %v", err)
	}
}

func TestBuildFailureDoesNotCreateSuccessfulChecksums(t *testing.T) {
	output := filepath.Join(t.TempDir(), "dist")
	want := errors.New("compiler unavailable")
	err := createPackages(fixture(t), output, "1.0.0-rc.1", func(_, _, _ string, _ platform) error { return want }, io.Discard)
	if !errors.Is(err, want) {
		t.Fatalf("erro do compilador perdido: %v", err)
	}
	files, err := os.ReadDir(output)
	if err != nil || len(files) != 0 {
		t.Fatalf("build falhou mas produziu arquivos: %v %v", files, err)
	}
}

func TestReadAssetsWithoutLicense(t *testing.T) {
	root := fixture(t)
	if err := os.Remove(filepath.Join(root, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	assets, err := readAssets(root)
	if err != nil {
		t.Fatalf("projeto sem licença deve poder ser empacotado: %v", err)
	}
	if len(assets) != 5 {
		t.Fatalf("quantidade inesperada de arquivos sem licença: %d", len(assets))
	}
	for _, file := range assets {
		if file.name == "LICENSE" {
			t.Fatal("licença foi inventada")
		}
	}
	// A present license must still be a regular, readable file.
	if err := os.Mkdir(filepath.Join(root, "LICENSE"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := readAssets(root); err == nil {
		t.Fatal("diretório LICENSE foi aceito como documento")
	}
}
