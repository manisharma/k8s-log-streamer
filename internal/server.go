package internal

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/manisharma/k8s-log-streamer/pkg/common"

	"github.com/rs/zerolog"
	"k8s.io/client-go/kubernetes"
)

type Server struct {
	cfg              common.LogsStreamerConfig
	kill             chan struct{}
	logBatch         []entry
	logBatchMutex    *sync.Mutex
	httpClient       *http.Client
	keywords         [][]byte
	newLine          []byte
	failedRegex      *regexp.Regexp
	errorRegex       *regexp.Regexp
	coreFilterRegex  *regexp.Regexp
	filtersRegex     []*regexp.Regexp
	regexp5xxValue   *regexp.Regexp
	regexp4xxValue   *regexp.Regexp
	k8sClient        *kubernetes.Clientset
	ledger           map[string]streamCtx
	ledgerLocker     *sync.Mutex
	stream           chan streamable
	logger           zerolog.Logger
	isNativeInformer bool
}

type Option func(*Server)

func Withk8sClient(k8sClient *kubernetes.Clientset) Option {
	return func(s *Server) {
		s.k8sClient = k8sClient
	}
}

func WithLogger(logger zerolog.Logger) Option {
	return func(s *Server) {
		s.logger = logger.With().Str("module", "k8sLogsStreamer").Logger()
	}
}

func WithNativeInformer(isNativeInformer bool) Option {
	return func(s *Server) {
		s.isNativeInformer = isNativeInformer
	}
}

func NewServer(cfg common.LogsStreamerConfig, options ...Option) *Server {
	s := &Server{
		cfg:           cfg,
		kill:          make(chan struct{}),
		logBatchMutex: &sync.Mutex{},
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		newLine:       []byte("\n"),
		failedRegex:   regexp.MustCompile(`(?i)failed`),
		errorRegex:    regexp.MustCompile(`(?i)error`),
		ledger:        make(map[string]streamCtx),
		ledgerLocker:  &sync.Mutex{},
		// stream:           make(chan streamable, 2000),
		stream:           make(chan streamable),
		logger:           zerolog.New(os.Stdout).With().Str("module", "k8sLogsStreamer").Logger(),
		isNativeInformer: false,
	}
	s.keywords = make([][]byte, 0, len(cfg.Keywords))
	s.filtersRegex = make([]*regexp.Regexp, 0, len(cfg.Keywords))

	var (
		has4xx           = false
		has5xx           = false
		regexp5XXValue   = `\b(500|501|502|503|504|505|506|507|508|510|511)\b`
		regexp4XXValue   = `\b4(?:[01][0-9]|2[1-689]|31|51)\b`
		filteredKeywords = make([]string, 0, len(cfg.Keywords))
	)

	if s.cfg.SecondsToLookBackForLogs <= 0 {
		s.cfg.SecondsToLookBackForLogs = 30
	}

	for _, keyword := range cfg.Keywords {
		if strings.EqualFold(keyword, "4xx") {
			has4xx = true
			s.regexp4xxValue = regexp.MustCompile(regexp4XXValue)
			continue
		}
		if strings.EqualFold(keyword, "5xx") {
			has5xx = true
			s.regexp5xxValue = regexp.MustCompile(regexp5XXValue)
			continue
		}
		filteredKeywords = append(filteredKeywords, keyword)
		s.filtersRegex = append(s.filtersRegex, regexp.MustCompile(`(?i)`+regexp.QuoteMeta(keyword)))
		s.keywords = append(s.keywords, []byte(keyword))
	}

	var regexpBuilder strings.Builder
	if has4xx || has5xx {
		if has4xx {
			s.filtersRegex = append(s.filtersRegex, s.regexp4xxValue)
		}
		if has5xx {
			s.filtersRegex = append(s.filtersRegex, s.regexp5xxValue)
		}
		if has4xx && has5xx {
			regexpBuilder.WriteString(regexp4XXValue + "|" + regexp5XXValue)
		}
		if !has4xx && has5xx {
			regexpBuilder.WriteString(regexp5XXValue)
		}
		if has4xx && !has5xx {
			regexpBuilder.WriteString(regexp4XXValue)
		}
	}

	if len(filteredKeywords) > 0 {
		if has4xx || has5xx {
			regexpBuilder.WriteString(`|`)
		}
		regexpBuilder.WriteString(`(?i)` + strings.Join(filteredKeywords, "|"))
	}

	s.coreFilterRegex = regexp.MustCompile(regexpBuilder.String())
	s.logBatch = make([]entry, 0, s.cfg.BatchSize)
	for _, option := range options {
		option(s)
	}
	s.logger.Debug().Str("operator", s.cfg.Operator).Strs("keywords", cfg.Keywords).Int("filtersCount", len(s.filtersRegex)).Msg("initialized")
	return s
}

func (s *Server) Start(ctx context.Context) error {
	if s.k8sClient == nil {
		cfg, err := s.extractConfig()
		if err != nil {
			return fmt.Errorf("s.extractConfig() failed, error: %v", err)
		}
		s.k8sClient, err = kubernetes.NewForConfig(cfg)
		if err != nil {
			return fmt.Errorf("kubernetes.NewForConfig(cfg) failed, error: %v", err)
		}
	}
	return s.start(ctx)
}

func (s *Server) Stop() {
	close(s.kill)
}

func (s *Server) AddFn(ctx context.Context, obj any) {
	s.addFn(ctx, obj)
}

func (s *Server) UpdateFn(ctx context.Context, oldObj, newObj any) {
	s.updateFn(ctx, newObj)
}

func (s *Server) DeleteFn(ctx context.Context, obj any) {
	s.deleteFn(ctx, obj)
}
