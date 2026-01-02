package common

import "time"

type LogsStreamerConfig struct {
	KubeConfigPath             string
	TargetURLWithHostAndScheme string
	Operator                   string
	NamespacesToInclude        []string
	NamespacesToExclude        []string
	Keywords                   []string
	PodLabelsToInclude         []string
	BatchSize                  int
	WorkerCount                int
	FlushInterval              time.Duration
	SecondsToLookBackForLogs   int
	RequestsPerSecond          int
	BurstCapacity              int
}
