package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/rs/zerolog"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

func (s *Server) extractConfig() (*rest.Config, error) {
	// local config
	if s.cfg.KubeConfigPath != "" {
		s.logger.Debug().Msgf("loading k8s configuration from %s", s.cfg.KubeConfigPath)
		config, err := clientcmd.BuildConfigFromFlags("", s.cfg.KubeConfigPath)
		if err != nil {
			s.logger.Error().Err(err).Str("kubeConfigPath", s.cfg.KubeConfigPath).Msg("clientcmd.BuildConfigFromFlags() failed")
		}
		return config, err
	}
	s.logger.Debug().Msgf("loading incluster configuration")
	// cluster config (uses service account token)
	config, err := rest.InClusterConfig()
	if err != nil {
		s.logger.Error().Err(err).Msg("rest.InClusterConfig() failed")
	}
	return config, err
}

func (s *Server) isPodCurated(pod *corev1.Pod) bool {
	if len(s.cfg.NamespacesToExclude) > 0 {
		if slices.Contains(s.cfg.NamespacesToExclude, pod.Namespace) {
			return false
		}
	}
	if len(s.cfg.NamespacesToInclude) > 0 {
		if !slices.Contains(s.cfg.NamespacesToInclude, pod.Namespace) {
			return false
		}
	}
	if len(s.cfg.PodLabelsToInclude) > 0 {
		for key, value := range pod.Labels {
			if slices.Contains(s.cfg.PodLabelsToInclude, key+"="+value) {
				return true
			}
		}
		return false
	}
	return true
}

func toStreamable(ctx context.Context, pod *corev1.Pod) streamable {
	p := streamable{ctx: ctx, name: pod.Name, namespace: pod.Namespace}
	p.containers = make([]streamableContainer, 0, len(pod.Status.ContainerStatuses))
	for _, container := range pod.Status.ContainerStatuses {
		p.containers = append(p.containers, streamableContainer{
			name:    container.Name,
			image:   container.Image,
			imageId: container.ImageID,
		})
	}
	return p
}

func (s *Server) addFn(ctx context.Context, obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		s.logger.Error().Msg("addFn: obj.(*corev1.Pod) failed")
		return
	}
	if pod == nil || !s.isPodCurated(pod) {
		return
	}
	name := extractFullyQualifiedName(pod)
	s.ledgerLocker.Lock()
	_, ok = s.ledger[name]
	if ok {
		s.ledgerLocker.Unlock()
		return
	}
	fnCtx, cancel := context.WithCancel(ctx)
	streamable := toStreamable(fnCtx, pod)
	s.ledger[name] = streamCtx{cancel: cancel, streamable: streamable}
	s.ledgerLocker.Unlock()
}

func (s *Server) updateFn(ctx context.Context, obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		s.logger.Error().Msg("updateFn: newObj.(*corev1.Pod) failed")
		return
	}
	if pod == nil || !s.isPodCurated(pod) {
		return
	}
	name := extractFullyQualifiedName(pod)
	s.ledgerLocker.Lock()
	_, ok = s.ledger[name]
	if ok {
		s.ledgerLocker.Unlock()
		return
	}
	fnCtx, cancel := context.WithCancel(ctx)
	streamable := toStreamable(fnCtx, pod)
	s.ledger[name] = streamCtx{cancel: cancel, streamable: streamable}
	s.ledgerLocker.Unlock()
}

func (s *Server) deleteFn(_ context.Context, obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		s.logger.Error().Msg("deleteFn: obj.(*corev1.Pod) failed")
		return
	}
	name := extractFullyQualifiedName(pod)
	s.ledgerLocker.Lock()
	existing, ok := s.ledger[name]
	if ok {
		existing.cancel()
		delete(s.ledger, name)
	}
	s.ledgerLocker.Unlock()
}

func (s *Server) start(ctx context.Context) error {

	go s.periodicDeltaFlusher(ctx)

	go s.periodicLedgerProcessor(ctx)

	for i := range s.cfg.WorkerCount {
		go s.processStream(i)
	}

	var factory informers.SharedInformerFactory

	if s.isNativeInformer {
		factory = informers.NewSharedInformerFactoryWithOptions(s.k8sClient, time.Minute*5)
		informer := factory.Core().V1().Pods()

		informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				s.addFn(ctx, obj)
			},
			UpdateFunc: func(_, newObj any) {
				s.updateFn(ctx, newObj)
			},
			DeleteFunc: func(obj any) {
				s.deleteFn(ctx, obj)
			},
		})

		factory.Start(s.kill)
		if !cache.WaitForNamedCacheSync("logs_streamer", s.kill, informer.Informer().HasSynced) {
			err := fmt.Errorf("cache.WaitForNamedCacheSync() failed")
			s.logger.Error().Msg(err.Error())
			return err
		}
	}

	<-s.kill

	if s.isNativeInformer {
		factory.Shutdown()
	}
	return nil
}

func (s *Server) periodicDeltaFlusher(ctx context.Context) {
	defer func() {
		// last opportunity to flush remaining logs, if any
		s.logger.Debug().Msg("flushing pendign buffer")
		s.flush()
	}()
	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.kill:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.flush()
		}
	}
}

func extractFullyQualifiedName(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	return pod.Namespace + "/" + pod.Name
}

func (s *Server) process(item streamable, logger zerolog.Logger) {
	// round robin through all containers
	for _, container := range item.containers {
		select {
		case <-item.ctx.Done():
			return
		default:
		}
		lgr := logger.With().Str("namespace", item.namespace).Str("pod", item.name).Str("container", container.name).Logger()
		lgr.Debug().Msg("streaming")
		logOptions := &corev1.PodLogOptions{Container: container.name}
		logOptions.SinceSeconds = func(i int64) *int64 { return &i }(30)
		req := s.k8sClient.CoreV1().Pods(item.namespace).GetLogs(item.name, logOptions)
		raw, err := req.Do(item.ctx).Raw()
		if err != nil {
			if err == item.ctx.Err() {
				return
			}
			lgr.Error().Err(err).Msg("streaming failed")
			continue
		}
		if curated, raw := s.areLogsCurated(raw); curated {
			s.pileUpOrFlush(entry{
				Namespace: item.namespace,
				Pod:       item.name,
				Container: container.name,
				Image:     container.image,
				Logs:      raw,
			})
		}
	}
}

func (s *Server) periodicLedgerProcessor(ctx context.Context) {
	var (
		ledgerCopy map[string]streamCtx
		ticker     = time.NewTicker(time.Second * 5)
		flushing   = false
	)
	s.logger.Debug().Msg("started periodicLedgerProcessor")
	defer func() {
		ticker.Stop()
		s.logger.Debug().Msg("exiting periodicLedgerProcessor")
	}()

	stream := func() {
		if flushing {
			return
		}
		s.ledgerLocker.Lock()
		// create copy of ledger to process outside lock
		ledgerCopy = make(map[string]streamCtx, len(s.ledger))
		maps.Copy(ledgerCopy, s.ledger)
		s.ledgerLocker.Unlock()
		flushing = true
		defer func() { flushing = false }()
		for _, item := range ledgerCopy {
			select {
			case <-s.kill:
				return
			case <-ctx.Done():
				return
			default:
			}
			s.stream <- item.streamable
		}
	}
	stream()

	for {
		select {
		case <-s.kill:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			stream()
		}
	}
}

func (s *Server) processStream(id int) {
	logger := s.logger.With().Int("workerId", id).Logger()
	// logger.Debug().Msg("worker supnned up")
	for item := range s.stream {
		s.process(item, logger)
	}
}

func (s *Server) meetsCriteria(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	if s.cfg.Operator == "and" {
		if !s.coreFilterRegex.Match(raw) {
			return false
		}
		// 4xx status code must be in conjunction with 'failed' or 'error'
		if s.regexp4xxValue != nil && s.regexp4xxValue.Match(raw) {
			// short circuit if either 'failed' or 'error' is present
			if s.failedRegex.Match(raw) || s.errorRegex.Match(raw) {
				return true
			}
		}
		// 5xx status code must be in conjunction with 'failed' or 'error'
		if s.regexp5xxValue != nil && s.regexp5xxValue.Match(raw) {
			// short circuit if either 'failed' or 'error' is present
			if s.failedRegex.Match(raw) || s.errorRegex.Match(raw) {
				return true
			}
		}
		// fallback all filters must match
		for _, filter := range s.filtersRegex {
			if !filter.Match(raw) {
				return false
			}
		}
		return true
	}
	return s.coreFilterRegex.Match(raw)
}

func (s *Server) areLogsCurated(raw []byte) (bool, []byte) {
	if !s.meetsCriteria(raw) {
		return false, nil
	}

	lines := bytes.Split(raw, s.newLine)
	filtered := make([][]byte, 0, len(lines))

	for _, line := range lines {
		if s.meetsCriteria(line) {
			filtered = append(filtered, line)
		}
	}

	if len(filtered) == 0 {
		return false, nil
	}

	return true, bytes.Join(filtered, s.newLine)
}

func (s *Server) pileUpOrFlush(logEntry entry) {
	s.logBatchMutex.Lock()
	s.logBatch = append(s.logBatch, logEntry)
	shouldFlush := len(s.logBatch) >= s.cfg.BatchSize
	s.logBatchMutex.Unlock()
	if shouldFlush {
		s.flush()
	}
}

func (s *Server) flush() {
	s.logBatchMutex.Lock()
	if len(s.logBatch) == 0 {
		s.logBatchMutex.Unlock()
		return
	}
	batchToSend := s.logBatch
	s.logBatch = s.logBatch[:0]
	s.logBatchMutex.Unlock()

	r, w := io.Pipe()
	go func() {
		defer w.Close()
		err := json.NewEncoder(w).Encode(batchToSend)
		if err != nil {
			s.logger.Error().Err(err).Msg("json.NewEncoder(w).Encode(batchToSend) failed")
			return
		}
	}()

	req, err := http.NewRequest(http.MethodPost, s.cfg.TargetURLWithHostAndScheme, r)
	if err != nil {
		s.logger.Error().Err(err).Msg("http.NewRequest() failed")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mit-Subscription-ID", "1")
	req.Header.Set("Mit-Org-ID", "1")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.Error().Err(err).Msg("s.httpClient.Do(req) failed")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.logger.Error().Err(err).Int("status", resp.StatusCode).Bytes("response", body).Msg("post failed")
	}
}
