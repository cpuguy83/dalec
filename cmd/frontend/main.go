package main

import (
	"context"
	_ "embed"
	"os"
	"os/exec"

	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/azlinux"
	"github.com/Azure/dalec/frontend/debian"
	"github.com/Azure/dalec/frontend/debug"
	"github.com/Azure/dalec/frontend/ubuntu"
	"github.com/Azure/dalec/frontend/windows"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
)

const (
	Package = "github.com/Azure/dalec/cmd/frontend"
)

func main() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))

	ctx := appcontext.Context()

	var err error
	if os.Getenv("_DALEC_DEBUG") == "1" {
		if os.Getenv("_DALEC_DEBUG_PHASE") == "2" {
			err = run(ctx)
		} else {
			err = runDebug(ctx)
		}
	}

	if err != nil {
		bklog.L.WithError(err).Fatal("Error running frontend")
		os.Exit(137)
	}
}

func run(ctx context.Context) error {
	var mux frontend.BuildMux

	mux.Add(debug.DebugRoute, debug.Handle, nil)

	if err := grpcclient.RunFromEnvironment(ctx, mux.Handler(
		// copy/paster's beware: [frontend.WithTargetForwardingHandler] should not be set except for the root dalec frontend.
		frontend.WithBuiltinHandler(azlinux.Mariner2TargetKey, azlinux.NewMariner2Handler()),
		frontend.WithBuiltinHandler(azlinux.AzLinux3TargetKey, azlinux.NewAzlinux3Handler()),
		frontend.WithBuiltinHandler(windows.DefaultTargetKey, windows.Handle),
		ubuntu.Handlers,
		debian.Handlers,
		frontend.WithTargetForwardingHandler,
	)); err != nil {
		bklog.L.WithError(err).Fatal("error running frontend")
		os.Exit(137)
	}
}

func runDebug(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "dlv", "exec", "/frontend")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "_DALEC_DEBUG_PHASE=2")
	return cmd.Run()
}
