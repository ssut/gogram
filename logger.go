package gogram

import (
	"github.com/Sirupsen/logrus"
	prefixed "github.com/x-cray/logrus-prefixed-formatter"
)

var log = logrus.New()

func initLogger(debug bool) {
	log.Formatter = new(prefixed.TextFormatter)
	level := logrus.WarnLevel
	if debug {
		level = logrus.DebugLevel
	}
	log.Level = level
}
