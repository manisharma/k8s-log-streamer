package internal

import (
	"context"
)

type streamCtx struct {
	cancel context.CancelFunc
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
