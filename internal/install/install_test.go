package install

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"recon-framework/internal/toolchain"
)

func TestSelectAssetPrefersTargetPlatform(t *testing.T) {
	t.Parallel()

	spec := toolchain.ToolSpec{Name: "dnsx", Binary: "dnsx"}
	assets := []releaseAsset{
		{Name: "dnsx_1.0.0_linux_amd64.zip"},
		{Name: "dnsx_1.0.0_windows_amd64.zip"},
	}

	selected, err := selectAsset(spec, assets, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Name != "dnsx_1.0.0_windows_amd64.zip" {
		t.Fatalf("unexpected asset: %s", selected.Name)
	}
}

func TestAssetScoreDoesNotTreatDarwinAsWindows(t *testing.T) {
	t.Parallel()

	spec := toolchain.ToolSpec{Name: "amass", Binary: "amass"}
	windowsScore := assetScore(spec, "amass_windows_amd64.tar.gz", "windows", "amd64")
	darwinScore := assetScore(spec, "amass_darwin_amd64.tar.gz", "windows", "amd64")

	if windowsScore <= darwinScore {
		t.Fatalf("expected windows asset to score higher than darwin: windows=%d darwin=%d", windowsScore, darwinScore)
	}
}

func TestExtractZipWritesTargetBinary(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "dnsx.zip")
	destination := filepath.Join(tempDir, "dnsx")

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("dnsx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if err := extractAsset(archivePath, "dnsx_windows_amd64.zip", destination); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "binary" {
		t.Fatalf("unexpected extracted content: %s", string(content))
	}
}
