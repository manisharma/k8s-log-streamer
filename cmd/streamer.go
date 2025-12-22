package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/manisharma/k8s-log-streamer/pkg/common"
	"github.com/manisharma/k8s-log-streamer/pkg/streamer"

	"net/http"
	_ "net/http/pprof"

	"github.com/rs/zerolog"
)

func main() {
	var (
		kubeConfigPath             string
		namespacesToInclude        common.SliceFlag
		namespacesToExclude        common.SliceFlag
		keywords                   common.SliceFlag
		podLabelsToInclude         common.SliceFlag
		targetURLWithHostAndScheme string
		operator                   string
		batchSize                  int
		workerCount                int
		flushInterval              time.Duration
		ctx, cancel                = context.WithCancel(context.Background())
		logger                     = zerolog.New(os.Stdout)
	)
	defer cancel()

	flag.StringVar(&kubeConfigPath, "kubeconfigPath", "/Users/manish.sharma/.kube/config", "path to the kubeconfig file (if running outside the cluster)")
	flag.Var(&namespacesToInclude, "namespacesToInclude", "kubernetes namespace to include pods from during streaming")
	flag.Var(&namespacesToExclude, "namespacesToExclude", "kubernetes namespaces to exclude pods from during streaming")
	flag.Var(&keywords, "keywords", "keywords to filter logs")
	flag.Var(&podLabelsToInclude, "podLabelsToInclude", "labels to filter pods in or combination")
	flag.StringVar(&targetURLWithHostAndScheme, "targetURLWithHostAndScheme", "http://localhost:8000/curated_log_streamer", "target URL with host and scheme to send logs to")
	flag.StringVar(&operator, "operator", "or", "operator to use for filtering logs, eg: 'or', 'and'")
	flag.IntVar(&batchSize, "batchSize", 10, "number of log entries to batch before sending")
	flag.IntVar(&workerCount, "workerCount", 100, "number of concurrent workers to process log streams")
	flag.DurationVar(&flushInterval, "flushInterval", 5*time.Second, "interval to flush log batches")
	flag.Parse()

	if keywords.Len() == 0 {
		logger.Error().Msg("nothing to look for in logs")
		os.Exit(0)
	}

	if operator != "or" && operator != "and" {
		logger.Error().Msg("invalid operator, only 'or' and 'and' are supported")
		os.Exit(0)
	}

	profiler := &http.Server{Addr: ":6060"}
	go func() {
		logger.Debug().Msg("profiler listening to connections http://localhost:6060")
		if err := profiler.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("profiler.ListenAndServe() failed")
		}
	}()

	var (
		cfg = common.LogsStreamerConfig{
			KubeConfigPath:             kubeConfigPath,
			NamespacesToInclude:        namespacesToInclude,
			NamespacesToExclude:        namespacesToExclude,
			Keywords:                   keywords.Items(),
			PodLabelsToInclude:         podLabelsToInclude.Items(),
			TargetURLWithHostAndScheme: targetURLWithHostAndScheme,
			Operator:                   operator,
			BatchSize:                  batchSize,
			FlushInterval:              flushInterval,
			WorkerCount:                workerCount,
		}
		logStreamer = streamer.NewLogStreamer(cfg, streamer.WithLogger(logger))
		deathStream = make(chan os.Signal, 1)
	)

	go func() {
		signal.Notify(deathStream, os.Interrupt, syscall.SIGABRT, syscall.SIGTERM, syscall.SIGINT)
		<-deathStream
		logger.Debug().Msg("log streamer interrupted")
		signal.Stop(deathStream)
		profiler.Shutdown(ctx)
		logStreamer.Stop()
	}()

	logger.Debug().Msg("log streamer is streaming...")
	err := logStreamer.Start(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("logStreamer.Start(ctx) failed")
	}
	cancel()
}
