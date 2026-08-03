package api

import "netwatch/internal/probe"

type Handler struct {
	service    *probe.Service
	speedSlots chan struct{}
}

func NewHandler(service *probe.Service) *Handler {
	return &Handler{service: service, speedSlots: make(chan struct{}, maxConcurrentSpeedStreams)}
}
