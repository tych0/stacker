package log

import (
	"github.com/apex/log"
)

/*
var (
	Logger log.Interface
)

func init() {
	Logger = log.Log
}

type logFilterer struct {
	allowed string
	level   log.Level
	handler log.HandlerFunc
}

func New(allowed string, level, log.Level, handler HandlerFunc) log.Interface {
	return logFilterer(allowed, level, handler)
}

func Debugf()
*/

var thisIsAStackerLog struct{}

func addStackerLogSentinel(e *log.Entry) *log.Entry {
	return e.WithField("isStacker", &thisIsAStackerLog)
}

func isStackerLog(e *log.Entry) bool {
	v, ok := e.Fields["isStacker"]
	return ok && v == &thisIsAStackerLog
}

type stackerLogFilterer struct {
	underlying log.Handler
}

func (h stackerLogFilterer) HandleLog(e *log.Entry) error {
	if !isStackerLog(e) {
		return nil
	}

	delete(e.Fields, "isStacker")

	return h.underlying.HandleLog(e)
}

func FilterNonStackerLogs(handler log.Handler) {
	log.SetHandler(stackerLogFilterer{handler})
}

func Debugf(msg string, v ...interface{}) {
	addStackerLogSentinel(log.NewEntry(log.Log.(*log.Logger))).Debugf(msg, v...)
}

func Infof(msg string, v ...interface{}) {
	addStackerLogSentinel(log.NewEntry(log.Log.(*log.Logger))).Infof(msg, v...)
}

func Warnf(msg string, v ...interface{}) {
	addStackerLogSentinel(log.NewEntry(log.Log.(*log.Logger))).Warnf(msg, v...)
}

func Errorf(msg string, v ...interface{}) {
	addStackerLogSentinel(log.NewEntry(log.Log.(*log.Logger))).Errorf(msg, v...)
}

func Fatalf(msg string, v ...interface{}) {
	addStackerLogSentinel(log.NewEntry(log.Log.(*log.Logger))).Fatalf(msg, v...)
}
