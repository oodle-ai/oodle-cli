package grafana

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"

)

func CompressDirectory(
	dir string,
	outputPath string,
) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return wrapErr(err, "failed to create tar.gz file")
	}
	defer file.Close()

	gzWriter := gzip.NewWriter(file)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	baseDir := filepath.Dir(dir)
	return filepath.Walk(
		dir,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			relPath, err := filepath.Rel(baseDir, path)
			if err != nil {
				return wrapErr(err, "failed to get relative path")
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return wrapErr(err, "failed to create tar header")
			}
			header.Name = relPath

			if err := tarWriter.WriteHeader(header); err != nil {
				return wrapErr(err, "failed to write tar header")
			}

			if !info.Mode().IsRegular() {
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				return wrapErr(err, "failed to open file for tar")
			}
			defer f.Close()

			if _, err := io.Copy(tarWriter, f); err != nil {
				return wrapErr(err, "failed to copy file to tar")
			}

			return nil
		},
	)
}
