package internal

import (
	"context"
	"time"
)

type streamCtx struct {
	cancel         context.CancelFunc
	lastStreamedAt time.Time
	streamable
}

type entry struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
	Image     string `json:"image"`
	ImageId   string `json:"imageId"`
	Logs      any    `json:"logs"`
}

type streamable struct {
	ctx        context.Context
	name       string
	containers []streamableContainer
	namespace  string
}

type streamableContainer struct {
	name    string
	image   string
	imageId string
}
