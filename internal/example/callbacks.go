package example

import (
	"context"
	"log"

	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
)

type Callbacks struct {
	Signal         chan struct{}
	Debug          bool
	Fetches        int
	Requests       int
	DeltaRequests  int
	DeltaResponses int
}

func (cb *Callbacks) Report() {
	log.Printf("server callbacks fetches=%d requests=%d\n", cb.Fetches, cb.Requests)
}
func (cb *Callbacks) OnStreamOpen(_ context.Context, id int64, typ string) error {
	if cb.Debug {
		log.Printf("stream %d open for %s\n", id, typ)
	}
	return nil
}
func (cb *Callbacks) OnStreamClosed(id int64) {
	if cb.Debug {
		log.Printf("stream %d closed\n", id)
	}
}
func (cb *Callbacks) OnDeltaStreamOpen(_ context.Context, id int64, typ string) error {
	if cb.Debug {
		log.Printf("delta stream %d open for %s\n", id, typ)
	}
	return nil
}
func (cb *Callbacks) OnDeltaStreamClosed(id int64) {
	if cb.Debug {
		log.Printf("delta stream %d closed\n", id)
	}
}
func (cb *Callbacks) OnStreamRequest(id int64, req *discovery.DiscoveryRequest) error {
	log.Printf("------------- request for stream %d \n", id)
	return nil
}
func (cb *Callbacks) OnStreamResponse(ctx context.Context, id int64, req *discovery.DiscoveryRequest, res *discovery.DiscoveryResponse) {

	log.Printf(">>>>>>>>>>>>> response for stream %d\n", id)
	log.Printf("\n\n- response: %v\n\n", res)
}
func (cb *Callbacks) OnStreamDeltaResponse(id int64, req *discovery.DeltaDiscoveryRequest, res *discovery.DeltaDiscoveryResponse) {
	log.Printf(">>>>>>>>>>>>> delta response for stream %d\n", id)
	log.Printf("\n\n- response: %v\n\n", res)
}
func (cb *Callbacks) OnStreamDeltaRequest(id int64, req *discovery.DeltaDiscoveryRequest) error {
	log.Printf("------------- delta request for stream %d \n", id)
	return nil
}
func (cb *Callbacks) OnFetchRequest(_ context.Context, req *discovery.DiscoveryRequest) error {
	log.Printf("------------- fetch request (req: %v)\n", req)
	return nil
}
func (cb *Callbacks) OnFetchResponse(req *discovery.DiscoveryRequest, res *discovery.DiscoveryResponse) {
	log.Printf(">>>>>>>>>>>>> fetch response (req: %v) (res: %v)\n", req, res)
}
