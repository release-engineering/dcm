package main

import (
	"io/ioutil"

	"github.com/sirupsen/logrus"

	"github.com/release-engineering/dcm/internal/cmd"
)

func main() {
	// Silence the global logger
	logrus.SetOutput(ioutil.Discard)

	if err := cmd.Run(); err != nil {
		logrus.New().Fatal(err)
	}
}
