package isoeditor

import (
	"io"

	"github.com/openshift/assisted-image-service/pkg/overlay"
	"github.com/sirupsen/logrus"
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
	logrus.Debug("Isolate AIS filePath Phani ", file)
	logrus.Debug("Isolate AIS fileOffset Phani ", fileOffset)
	logrus.Debug("Isolate AIS fileLength Phani ", fileLength)

	expanded := false
	if minLength > fileLength {
		fileLength = minLength
		expanded = true
	}

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
