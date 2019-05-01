package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"k8s.io/component-base/logs"

	"github.com/Microsoft/KubeDevice/kubecri/cmd/app"
	"github.com/Microsoft/KubeDevice/logger"
)

func init() {
	logger.SetLogger()
}

func main() {
	rand.Seed(time.Now().UnixNano())

	command := app.NewCRICommand()
	logs.InitLogs()
	defer logs.FlushLogs()

	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
