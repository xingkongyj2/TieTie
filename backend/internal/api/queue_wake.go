package api

// Queue signals make newly written jobs visible immediately while the indexed
// polling path still recovers jobs after a process restart or missed signal.
func (s *Server) initQueueWakes() {
	s.wakeOnce.Do(func() {
		s.conversationWake = make(chan struct{}, 1)
		s.controlWake = make(chan struct{}, 1)
		s.memoryWake = make(chan struct{}, 1)
	})
}

func signalQueue(queue chan struct{}) {
	select {
	case queue <- struct{}{}:
	default:
	}
}

func (s *Server) wakeConversation() {
	s.initQueueWakes()
	signalQueue(s.conversationWake)
}

func (s *Server) wakeControl() {
	s.initQueueWakes()
	signalQueue(s.controlWake)
}

func (s *Server) wakeMemory() {
	s.initQueueWakes()
	signalQueue(s.memoryWake)
}
