package addon

import (
	"fmt"
	"os/exec"
)

type OLM struct{}

func NewOLM() *OLM {
	return &OLM{}
}

func (o *OLM) Setup(*Config) error {
	return nil
}

func (o *OLM) Install() error {
	out, err := exec.Command("operator-sdk", "olm", "install", "--verbose").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %#v", out, err)
	}
	return nil
}

func (o *OLM) Logs() error {
	return nil
}

func (o *OLM) Uninstall() error {
	out, err := exec.Command("operator-sdk", "olm", "uninstall", "--verbose").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %#v", out, err)
	}
	return nil
}
