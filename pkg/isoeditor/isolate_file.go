package isoeditor

import (
	"io"

	"github.com/openshift/assisted-image-service/pkg/overlay"
)

type FileData struct {
	Filename string
	Data     io.ReadCloser
}

func isolateISOFile(isoPath, file string, data overlay.OverlayReader, minLength int64) (FileData, bool, error) {
	fileOffset, fileLength, err := GetISOFileInfo(file, isoPath)
	if err != nil {
		return FileData{}, false, err
	}

	expanded := false
	if minLength > fileLength {
		fileLength = minLength
		expanded = true
	}

	// If we seek to the content Offset instead of fileOffset we will only get the required kargs or ignition data but we also need normal file reader
	if _, err := data.Seek(fileOffset, io.SeekStart); err != nil {
		return FileData{}, false, err
	}
	fileData := struct {
		io.Reader
		io.Closer
	}{
		Reader: io.LimitReader(data, fileLength),
		Closer: data,
	}

	return FileData{Filename: file, Data: fileData}, expanded, nil
}
