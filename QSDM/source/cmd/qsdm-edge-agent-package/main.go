package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-edge-agent-package:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("qsdm-edge-agent-package", flag.ContinueOnError)
	source := flags.String("source", "", "directory containing the Linux release files")
	output := flags.String("output", "", "tar.gz output path")
	root := flags.String("root", "", "top-level archive directory")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *source == "" || *output == "" || *root == "" {
		return errors.New("--source, --output, and --root are required")
	}
	if filepath.Base(*root) != *root || strings.ContainsAny(*root, `/\\`) {
		return errors.New("archive root must be a single directory name")
	}
	entries := []struct {
		name string
		mode int64
	}{
		{name: "README.md", mode: 0o644},
		{name: "qsdm-edge-agent", mode: 0o755},
		{name: "qsdm-edge-control", mode: 0o755},
		{name: "qsdm-edge-gpu-helper", mode: 0o755},
	}
	for _, entry := range entries {
		info, err := os.Stat(filepath.Join(*source, entry.name))
		if err != nil {
			return fmt.Errorf("release input %s: %w", entry.name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release input %s is not a regular file", entry.name)
		}
	}

	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(*output), ".qsdm-edge-agent-*.tar.gz")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	gzipWriter := gzip.NewWriter(temporary)
	gzipWriter.Header.ModTime = time.Unix(0, 0).UTC()
	gzipWriter.Header.OS = 3
	tarWriter := tar.NewWriter(gzipWriter)
	epoch := time.Unix(0, 0).UTC()
	if err := tarWriter.WriteHeader(&tar.Header{
		Name:     *root + "/",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
		ModTime:  epoch,
	}); err != nil {
		return closeArchive(temporary, gzipWriter, tarWriter, err)
	}
	for _, entry := range entries {
		path := filepath.Join(*source, entry.name)
		info, _ := os.Stat(path)
		header := &tar.Header{
			Name:     *root + "/" + entry.name,
			Mode:     entry.mode,
			Size:     info.Size(),
			Typeflag: tar.TypeReg,
			ModTime:  epoch,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return closeArchive(temporary, gzipWriter, tarWriter, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return closeArchive(temporary, gzipWriter, tarWriter, err)
		}
		_, copyErr := io.Copy(tarWriter, file)
		closeErr := file.Close()
		if copyErr != nil {
			return closeArchive(temporary, gzipWriter, tarWriter, copyErr)
		}
		if closeErr != nil {
			return closeArchive(temporary, gzipWriter, tarWriter, closeErr)
		}
	}
	if err := closeArchive(temporary, gzipWriter, tarWriter, nil); err != nil {
		return err
	}
	return replaceArchiveWithRetry(temporaryPath, *output)
}

func replaceArchiveWithRetry(source, destination string) error {
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Windows security scanners can hold a just-closed archive open long enough
	// to deny Rename even inside a private build directory. Copying to the final
	// staging name and syncing it is safe here because publication happens only
	// after an independent checksum pass. POSIX builds retain atomic rename.
	if runtime.GOOS == "windows" {
		if err := copyArchiveWithRetry(source, destination); err != nil {
			return err
		}
		_ = os.Remove(source)
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < 12; attempt++ {
		if err := os.Rename(source, destination); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	return fmt.Errorf("replace archive after retry: %w", lastErr)
}

func copyArchiveWithRetry(source, destination string) error {
	var lastErr error
	for attempt := 0; attempt < 12; attempt++ {
		if err := copyArchive(source, destination); err == nil {
			return nil
		} else {
			lastErr = err
			_ = os.Remove(destination)
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	return fmt.Errorf("copy archive after retry: %w", lastErr)
}

func copyArchive(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	if err = output.Sync(); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func closeArchive(file *os.File, gzipWriter *gzip.Writer, tarWriter *tar.Writer, prior error) error {
	tarErr := tarWriter.Close()
	gzipErr := gzipWriter.Close()
	syncErr := file.Sync()
	fileErr := file.Close()
	for _, err := range []error{prior, tarErr, gzipErr, syncErr, fileErr} {
		if err != nil {
			return err
		}
	}
	return nil
}
