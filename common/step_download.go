package common

import (
	"encoding/hex"
	"fmt"
	"github.com/mitchellh/multistep"
	"github.com/mitchellh/packer/packer"
	"log"
	"time"
)

// StepDownload downloads a remote file using the download client within
// this package. This step handles setting up the download configuration,
// progress reporting, interrupt handling, etc.
//
// Uses:
//   cache packer.Cache
//   ui    packer.Ui
type StepDownload struct {
	// The checksum and the type of the checksum for the download
	Checksum     string
	ChecksumType string

	// A short description of the type of download being done. Example:
	// "ISO" or "Guest Additions"
	Description string

	// The name of the key where the final path of the ISO will be put
	// into the state.
	ResultKey string

	// The path where the result should go, otherwise it goes to the
	// cache directory.
	TargetPath string

	// A list of URLs to attempt to download this thing.
	Url []string
}

func (s *StepDownload) Run(state map[string]interface{}) multistep.StepAction {
	cache := state["cache"].(packer.Cache)
	ui := state["ui"].(packer.Ui)

	var checksum []byte
	if s.Checksum != "" {
		var err error
		checksum, err = hex.DecodeString(s.Checksum)
		if err != nil {
			state["error"] = fmt.Errorf("Error parsing checksum: %s", err)
			return multistep.ActionHalt
		}
	}

	ui.Say(fmt.Sprintf("Downloading or copying %s", s.Description))

	var finalPath string
	for _, url := range s.Url {
		ui.Message(fmt.Sprintf("Downloading or copying: %s", url))

		targetPath := s.TargetPath
		if targetPath == "" {
			log.Printf("Acquiring lock to download: %s", url)
			targetPath = cache.Lock(url)
			defer cache.Unlock(url)
		}

		config := &DownloadConfig{
			Url:        url,
			TargetPath: targetPath,
			CopyFile:   false,
			Hash:       HashForType(s.ChecksumType),
			Checksum:   checksum,
		}

		path, err, retry := s.download(config, state)
		if err != nil {
			ui.Message(fmt.Sprintf("Error downloading: %s", err))
		}

		if !retry {
			return multistep.ActionHalt
		}

		if err == nil {
			finalPath = path
			break
		}
	}

	if finalPath == "" {
		err := fmt.Errorf("%s download failed.", s.Description)
		state["error"] = err
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	state[s.ResultKey] = finalPath
	return multistep.ActionContinue
}

func (s *StepDownload) Cleanup(map[string]interface{}) {}

func (s *StepDownload) download(config *DownloadConfig, state map[string]interface{}) (string, error, bool) {
	var path string
	ui := state["ui"].(packer.Ui)
	download := NewDownloadClient(config)

	downloadCompleteCh := make(chan error, 1)
	go func() {
		var err error
		path, err = download.Get()
		downloadCompleteCh <- err
	}()

	progressTicker := time.NewTicker(5 * time.Second)
	defer progressTicker.Stop()

	for {
		select {
		case err := <-downloadCompleteCh:
			if err != nil {
				return "", err, true
			}

			return path, nil, true
		case <-progressTicker.C:
			progress := download.PercentProgress()
			if progress >= 0 {
				ui.Message(fmt.Sprintf("Download progress: %d%%", progress))
			}
		case <-time.After(1 * time.Second):
			if _, ok := state[multistep.StateCancelled]; ok {
				ui.Say("Interrupt received. Cancelling download...")
				return "", nil, false
			}
		}
	}
}
