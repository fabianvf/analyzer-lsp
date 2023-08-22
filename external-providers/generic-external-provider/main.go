package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/bombsimon/logrusr/v3"
	"github.com/konveyor/analyzer-lsp/provider"
	"github.com/konveyor/generic-external-provider/pkg/generic"
	"github.com/sirupsen/logrus"
)

var (
	port = flag.Int("port", 0, "Port must be set")
)

func main() {
	flag.Parse()
	logrusLog := logrus.New()
	logrusLog.SetOutput(os.Stdout)
	logrusLog.SetFormatter(&logrus.TextFormatter{})
	// need to do research on mapping in logrusr to level here TODO
	logrusLog.SetLevel(logrus.Level(5))

	log := logrusr.New(logrusLog)

	client := generic.NewGenericProvider()

	if port == nil || *port == 0 {
		panic(fmt.Errorf("must pass in the port for the external provider"))
	}

	s := provider.NewServer(client, *port, log)
	ctx := context.TODO()
	s.Start(ctx)
	// serviceClient, err := client.Init(ctx, log, provider.InitConfig{
	// 	Location:     "/home/fabian/projects/github.com/konveyor/analyzer-lsp/examples/golang",
	// 	AnalysisMode: "full",
	// 	ProviderSpecificConfig: map[string]interface{}{
	// 		"name":          "go",
	// 		"lspServerPath": "gopls",
	// 		"lspArgs":       []interface{}{"-vv", "-logfile", "go-debug.log", "-rpc.trace"},
	// 	},
	// })
	// if err != nil {
	// 	panic(err)
	// }

	// response, err := serviceClient.Evaluate("referenced", []byte(`{"referenced":{pattern: ".*v1beta1.CustomResourceDefinition"}}`))
	// if err != nil {
	// 	panic(err)
	// }
	// fmt.Println(response)
}
