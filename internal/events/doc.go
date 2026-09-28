package events

// Intended usage from internal/httpapi (M2-106), sketched here rather than
// as a runnable example: a live SSE stream is just a loop over the channel
// Subscribe returns, with a heartbeat so a proxy or the browser doesn't
// time out an idle connection out from under it.
//
//	func (s *Server) handleEventsStream(w http.ResponseWriter, r *http.Request) {
//		flusher, ok := w.(http.Flusher)
//		if !ok {
//			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
//			return
//		}
//
//		ch, unsubscribe := s.events.Subscribe(r.Context())
//		defer unsubscribe()
//
//		w.Header().Set("Content-Type", "text/event-stream")
//		w.Header().Set("Cache-Control", "no-cache")
//		w.Header().Set("Connection", "keep-alive")
//
//		ticker := time.NewTicker(15 * time.Second)
//		defer ticker.Stop()
//
//		for {
//			select {
//			case ev, ok := <-ch:
//				if !ok { // Hub closed the channel: ctx is done, stop streaming.
//					return
//				}
//				payload, err := json.Marshal(ev)
//				if err != nil {
//					continue // never let one bad row kill the stream
//				}
//				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, payload)
//				flusher.Flush()
//			case <-ticker.C:
//				fmt.Fprint(w, ": keep-alive\n\n")
//				flusher.Flush()
//			case <-r.Context().Done():
//				return
//			}
//		}
//	}
//
// A slow browser tab never stalls other subscribers or the writer that
// called Emit: Hub.Publish only ever does non-blocking sends, dropping the
// oldest buffered event for a subscriber that has fallen behind.
