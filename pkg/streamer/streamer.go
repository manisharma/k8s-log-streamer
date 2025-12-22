package streamer

import (
	"context"

	"github.com/manisharma/k8s-log-streamer/internal"
	"github.com/manisharma/k8s-log-streamer/pkg/common"

	"github.com/rs/zerolog"
	"k8s.io/client-go/kubernetes"
)

type Option func(*LogsStreamer)

func Withk8sClient(k8sClient *kubernetes.Clientset) Option {
	return func(s *LogsStreamer) {
		s.k8sClient = k8sClient
	}
}

func WithLogger(logger zerolog.Logger) Option {
	return func(s *LogsStreamer) {
		s.logger = logger
	}
}

type LogsStreamer struct {
	k8sClient *kubernetes.Clientset
	logger    zerolog.Logger
	server    *internal.Server
}

func NewLogStreamer(cfg common.LogsStreamerConfig, options ...Option) *LogsStreamer {
	l := &LogsStreamer{}
	for _, option := range options {
		option(l)
	}
	var internalOptions = make([]internal.Option, 0, 2)
	internalOptions = append(internalOptions, internal.WithLogger(l.logger))
	if l.k8sClient != nil {
		internalOptions = append(internalOptions, internal.Withk8sClient(l.k8sClient))
	}
	l.server = internal.NewServer(cfg, internalOptions...)
	return l
}

// blocking call
func (s *LogsStreamer) Start(ctx context.Context) error {
	return s.server.Start(ctx)
}

// graceful shutdown
func (s *LogsStreamer) Stop() {
	s.server.Stop()
}

func (s *LogsStreamer) AddFn(ctx context.Context, obj any) {
	s.server.AddFn(ctx, obj)
}

func (s *LogsStreamer) UpdateFn(ctx context.Context, oldObj, newObj any) {
	s.server.UpdateFn(ctx, oldObj, newObj)
}

func (s *LogsStreamer) DeleteFn(ctx context.Context, obj any) {
	s.server.DeleteFn(ctx, obj)
}
