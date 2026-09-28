package server_test

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strconv"
	"testing"

	"google.golang.org/grpc"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/log"
	"github.com/envoyproxy/go-control-plane/pkg/server/config"
	"github.com/envoyproxy/go-control-plane/pkg/server/sotw/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
)

// benchSnapshot is a cache.ResourceSnapshot with no resources that has one
// version for every type URL except the ones in open, which have the empty
// version, so the cache sends no response to a watch for them.
type benchSnapshot struct {
	version string
	open    map[string]struct{}
}

var _ cache.ResourceSnapshot = benchSnapshot{}

func (s benchSnapshot) GetVersion(typeURL string) string {
	if _, ok := s.open[typeURL]; ok {
		return ""
	}
	return s.version
}

func (benchSnapshot) GetResourcesAndTTL(string) map[string]types.ResourceWithTTL { return nil }
func (benchSnapshot) GetResources(string) map[string]types.Resource              { return nil }
func (benchSnapshot) ConstructVersionMap() error                                 { return nil }
func (benchSnapshot) GetVersionMap(string) map[string]string                     { return nil }

type benchHash struct{}

func (benchHash) ID(node *core.Node) string { return node.GetId() }

// benchStream is an ADS stream whose Send hands the response to the benchmark.
type benchStream struct {
	grpc.ServerStream

	ctx  context.Context
	recv chan *discovery.DiscoveryRequest
	sent chan *discovery.DiscoveryResponse
}

func newBenchStream(ctx context.Context, requests, responses int) *benchStream {
	return &benchStream{
		ctx:  ctx,
		recv: make(chan *discovery.DiscoveryRequest, requests),
		sent: make(chan *discovery.DiscoveryResponse, responses),
	}
}

func (s *benchStream) Context() context.Context { return s.ctx }

func (s *benchStream) Send(resp *discovery.DiscoveryResponse) error {
	select {
	case s.sent <- resp:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *benchStream) Recv() (*discovery.DiscoveryRequest, error) {
	select {
	case req := <-s.recv:
		return req, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func benchTypeURLs(n int) []string {
	urls := make([]string, n)
	for i := range urls {
		urls[i] = fmt.Sprintf("type.example.com/bench.Type%03d", i)
	}
	return urls
}

func benchRequests(nodeID string, urls []string) []*discovery.DiscoveryRequest {
	node := &core.Node{Id: nodeID}
	reqs := make([]*discovery.DiscoveryRequest, len(urls))
	for i, u := range urls {
		reqs[i] = &discovery.DiscoveryRequest{Node: node, TypeUrl: u}
	}
	return reqs
}

// benchGrow opens an ordered ADS stream that subscribes to n type URLs and waits
// until the server has created every watch. With open set, the watches for all
// but the last type URL stay open in the cache; otherwise every watch receives
// a response when it is created.
func benchGrow(b *testing.B, n int, open bool) {
	b.Helper()

	urls := benchTypeURLs(n)
	snap := benchSnapshot{version: "v1"}
	want := n
	if open {
		snap.open = make(map[string]struct{}, n)
		for _, u := range urls[:n-1] {
			snap.open[u] = struct{}{}
		}
		want = 1 // the last request receives a response after every earlier request is handled
	}
	reqs := benchRequests("node", urls)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc := cache.NewSnapshotCache(true, benchHash{}, log.NewDefaultLogger())
	if err := sc.SetSnapshot(ctx, "node", snap); err != nil {
		b.Fatal(err)
	}
	srv := server.NewServer(ctx, sc, server.CallbackFuncs{}, sotw.WithOrderedADS())

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		sctx, scancel := context.WithCancel(ctx)
		str := newBenchStream(sctx, n, n)
		for _, r := range reqs {
			str.recv <- r
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = srv.StreamAggregatedResources(str)
		}()
		for range want {
			<-str.sent
		}
		scancel()
		<-done
	}
}

// BenchmarkOrderedADSStreamSetup opens and closes an ordered ADS stream with 12
// type URLs, each with a response when its watch is created.
func BenchmarkOrderedADSStreamSetup(b *testing.B) { benchGrow(b, 12, false) }

// BenchmarkOrderedADSGrowth opens and closes an ordered ADS stream that
// subscribes to n type URLs, up to the default limit of
// config.DefaultOrderedMaxTypes.
func BenchmarkOrderedADSGrowth(b *testing.B) {
	for _, open := range []bool{false, true} {
		name := "answered"
		if open {
			name = "open"
		}
		for _, n := range []int{13, 25, config.DefaultOrderedMaxTypes} {
			b.Run(fmt.Sprintf("%s/types=%d", name, n), func(b *testing.B) { benchGrow(b, n, open) })
		}
	}
}

// BenchmarkOrderedADSSteadyState sets a new snapshot on a stream with 12 type
// URLs, reads the 12 responses and acknowledges each of them.
func BenchmarkOrderedADSSteadyState(b *testing.B) {
	const n = 12
	urls := make([]string, 0, n)
	for typ := range types.UnknownType {
		u, err := cache.GetResponseTypeURL(typ)
		if err != nil {
			b.Fatal(err)
		}
		urls = append(urls, u)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc := cache.NewSnapshotCache(true, benchHash{}, log.NewDefaultLogger())
	if err := sc.SetSnapshot(ctx, "node", benchSnapshot{version: "0"}); err != nil {
		b.Fatal(err)
	}
	srv := server.NewServer(ctx, sc, server.CallbackFuncs{}, sotw.WithOrderedADS())
	str := newBenchStream(ctx, 2*n, 2*n)
	for _, r := range benchRequests("node", urls) {
		str.recv <- r
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.StreamAggregatedResources(str)
	}()

	node := &core.Node{Id: "node"}
	cycle := func() {
		for range n {
			resp := <-str.sent
			str.recv <- &discovery.DiscoveryRequest{
				Node: node, TypeUrl: resp.GetTypeUrl(), VersionInfo: resp.GetVersionInfo(), ResponseNonce: resp.GetNonce(),
			}
		}
	}
	cycle()

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		version := strconv.Itoa(i + 1)
		if err := sc.SetSnapshot(ctx, "node", benchSnapshot{version: version}); err != nil {
			b.Fatal(err)
		}
		cycle()
	}
	b.StopTimer()
	cancel()
	<-done
}

// BenchmarkOrderedADSIdleStreams holds 1000 idle ordered ADS streams with 12
// open watches each and reports the memory they retain per stream.
func BenchmarkOrderedADSIdleStreams(b *testing.B) {
	const streams, n = 1000, 12
	urls := benchTypeURLs(n)

	var heap, stack float64
	for range b.N {
		ctx, cancel := context.WithCancel(context.Background())
		sc := cache.NewSnapshotCache(true, benchHash{}, log.NewDefaultLogger())
		srv := server.NewServer(ctx, sc, server.CallbackFuncs{}, sotw.WithOrderedADS())

		var before goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&before)

		dones := make([]chan struct{}, streams)
		for i := range streams {
			id := "node" + strconv.Itoa(i)
			str := newBenchStream(ctx, n, 1)
			for _, r := range benchRequests(id, urls) {
				str.recv <- r
			}
			dones[i] = make(chan struct{})
			go func() {
				defer close(dones[i])
				_ = srv.StreamAggregatedResources(str)
			}()
		}
		for i := range streams {
			for {
				info := sc.GetStatusInfo("node" + strconv.Itoa(i))
				if info != nil && info.GetNumWatches() == n {
					break
				}
				goruntime.Gosched()
			}
		}

		var after goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&after)
		heap = float64(after.HeapAlloc-before.HeapAlloc) / streams
		stack = float64(after.StackInuse-before.StackInuse) / streams

		cancel()
		for _, d := range dones {
			<-d
		}
	}
	b.ReportMetric(heap, "heap-B/stream")
	b.ReportMetric(stack, "stack-B/stream")
}
