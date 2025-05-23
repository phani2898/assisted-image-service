package isoeditor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/openshift/assisted-image-service/pkg/overlay"
	"github.com/sirupsen/logrus"
)

const (
	defaultGrubFilePath     = "/EFI/redhat/grub.cfg"
	defaultIsolinuxFilePath = "/isolinux/isolinux.cfg"
	kargsConfigFilePath     = "/coreos/kargs.json"
)

type FileReader func(isoPath, filePath string) ([]byte, error)

func kargsFiles(isoPath string, fileReader FileReader) ([]string, error) {
	kargsData, err := fileReader(isoPath, kargsConfigFilePath)
	if err != nil {
		// If the kargs file is not found, it is probably iso for old iso version which the file does not exist.  Therefore,
		// default is returned
		return []string{defaultGrubFilePath, defaultIsolinuxFilePath}, nil
	}
	var kargsConfig struct {
		Files []struct {
			Path *string
		}
	}
	if err := json.Unmarshal(kargsData, &kargsConfig); err != nil {
		return nil, err
	}
	var ret []string
	for _, file := range kargsConfig.Files {
		if file.Path != nil {
			ret = append(ret, *file.Path)
		}
	}
	return ret, nil
}

func KargsFiles(isoPath string) ([]string, error) {
	return kargsFiles(isoPath, ReadFileFromISO)
}

func readerForKargsS390x(isoPath string, filePath string, base io.ReadSeeker, contentReader *bytes.Reader) (overlay.OverlayReader, error) {
	// Get the fileOffset in ISO
	fileOffset, _, err := GetISOFileInfo(filePath, isoPath)
	if err != nil {
		return nil, err
	}
	logrus.Debug("readerForKargsS390x AIS fileOffset Phani ", fileOffset)

	// Read the kargs.json file content from the ISO
	kargsData, err := ReadFileFromISO(isoPath, kargsConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read kargs config: %w", err)
	}

	// Loading the kargs config JSON file
	var kargsConfig struct {
		Default string `json:"default"`
		Files   []struct {
			Path   string `json:"path"`
			Offset int64  `json:"offset"`
			End    string `json:"end"`
			Pad    string `json:"pad"`
		} `json:"files"`
		Size int `json:"size"`
	}
	if err := json.Unmarshal(kargsData, &kargsConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal kargs config: %w", err)
	}

	// Finding offset for the target filePath
	var kargsOffset int64
	var found bool
	for _, file := range kargsConfig.Files {
		if file.Path == filePath {
			kargsOffset = file.Offset
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("file %s not found in kargs config", filePath)
	}

	// Calculate the extraKargsOffset
	existingKargs := []byte(kargsConfig.Default)
	appendKargsOffset := fileOffset + kargsOffset + int64(len(existingKargs))
	logrus.Debug("readerForKargsS390x AIS appendKargsOffset Phani ", fileOffset)

	rdOverlay := overlay.Overlay{
		Reader: contentReader,
		Offset: appendKargsOffset,
		Length: contentReader.Size(),
	}
	r, err := overlay.NewOverlayReader(base, rdOverlay)
	if err != nil {
		return nil, err
	}

	return r, nil
}

func kargsFileData(isoPath string, file string, appendKargs []byte) (FileData, error) {
	baseISO, err := os.Open(isoPath)
	if err != nil {
		return FileData{}, err
	}

	var iso overlay.OverlayReader

	if strings.Contains(isoPath, "s390x") {
		iso, err = readerForKargsS390x(isoPath, file, baseISO, bytes.NewReader(appendKargs))
	} else {
		iso, err = readerForKargsContent(isoPath, file, baseISO, bytes.NewReader(appendKargs))
	}
	if err != nil {
		baseISO.Close()
		return FileData{}, err
	}

	fileData, _, err := isolateISOFile(isoPath, file, iso, 0)
	if err != nil {
		iso.Close()
		return FileData{}, err
	}
	logrus.Debug("AIS fileData Phani ", fileData.Data)

	return fileData, nil
}

// NewKargsReader returns the filename within an ISO and the new content of
// the file(s) containing the kernel arguments, with additional arguments
// appended.
func NewKargsReader(isoPath string, appendKargs string) ([]FileData, error) {
	if appendKargs == "" || appendKargs == "\n" {
		return nil, nil
	}
	appendData := []byte(appendKargs)
	if appendData[len(appendData)-1] != '\n' && !strings.Contains(isoPath, "s390x") {
		appendData = append(appendData, '\n')
	}

	files, err := KargsFiles(isoPath)
	if err != nil {
		return nil, err
	}

	output := []FileData{}
	for i, f := range files {
		data, err := kargsFileData(isoPath, f, appendData)
		if err != nil {
			for _, fd := range output[:i] {
				fd.Data.Close()
			}
			return nil, err
		}

		output = append(output, data)
	}
	return output, nil
}

func kargsEmbedAreaBoundariesFinder(isoPath, filePath string, fileBoundariesFinder BoundariesFinder, fileReader FileReader) (int64, int64, error) {
	start, _, err := fileBoundariesFinder(filePath, isoPath)
	if err != nil {
		return 0, 0, err
	}

	b, err := fileReader(isoPath, filePath)
	if err != nil {
		return 0, 0, err
	}

	re := regexp.MustCompile(`(\n#*)# COREOS_KARG_EMBED_AREA`)
	submatchIndexes := re.FindSubmatchIndex(b)
	if len(submatchIndexes) != 4 {
		return 0, 0, errors.New("failed to find COREOS_KARG_EMBED_AREA")
	}
	return start + int64(submatchIndexes[2]), int64(submatchIndexes[3] - submatchIndexes[2]), nil
}

func createKargsEmbedAreaBoundariesFinder() BoundariesFinder {
	return func(filePath, isoPath string) (int64, int64, error) {
		return kargsEmbedAreaBoundariesFinder(isoPath, filePath, GetISOFileInfo, ReadFileFromISO)
	}
}

func readerForKargsContent(isoPath string, filePath string, base io.ReadSeeker, contentReader *bytes.Reader) (overlay.OverlayReader, error) {
	return readerForContent(isoPath, filePath, base, contentReader, createKargsEmbedAreaBoundariesFinder())
}

type kernelArgument struct {
	// The operation to apply on the kernel argument.
	// Enum: [append replace delete]
	Operation string `json:"operation,omitempty"`

	// Kernel argument can have the form <parameter> or <parameter>=<value>. The following examples should
	// be supported:
	// rd.net.timeout.carrier=60
	// isolcpus=1,2,10-20,100-2000:2/25
	// quiet
	// The parsing by the command line parser in linux kernel is much looser and this pattern follows it.
	Value string `json:"value,omitempty"`
}

type kernelArguments []*kernelArgument

func KargsToStr(args []string) (string, error) {
	var kargs kernelArguments
	for _, s := range args {
		kargs = append(kargs, &kernelArgument{
			Operation: "append",
			Value:     s,
		})
	}
	b, err := json.Marshal(&kargs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal kernel arguments %v", err)
	}
	return string(b), nil
}

func StrToKargs(kargsStr string) ([]string, error) {
	var kargs kernelArguments
	if err := json.Unmarshal([]byte(kargsStr), &kargs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal kernel arguments %v", err)
	}
	var args []string
	for _, arg := range kargs {
		if arg.Operation != "append" {
			return nil, fmt.Errorf("only 'append' operation is allowed.  got %s", arg.Operation)
		}
		args = append(args, arg.Value)
	}
	return args, nil
}
