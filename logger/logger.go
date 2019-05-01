package logger

import (
	"fmt"

	"github.com/Microsoft/KubeDevice-API/pkg/utils"
	"k8s.io/klog"
)

func LogV(level int) bool {
	return bool(klog.V(klog.Level(level)))
}

func Log(level int, format string, args ...interface{}) {
	if klog.V(klog.Level(level)) {
		str := fmt.Sprintf(format, args...)
		klog.InfoDepth(1, str)
	}
}

func Error(format string, args ...interface{}) {
	str := fmt.Sprintf(format, args...)
	klog.ErrorDepth(1, str)
}

func Warning(format string, args ...interface{}) {
	str := fmt.Sprintf(format, args...)
	klog.WarningDepth(1, str)
}

func SetLogger() {
	utils.Logb = LogV
	utils.Logf = Log
	utils.Errorf = Error
	utils.Warningf = Warning
}
