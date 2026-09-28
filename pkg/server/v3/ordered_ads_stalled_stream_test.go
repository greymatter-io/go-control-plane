package server_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/log"
	"github.com/envoyproxy/go-control-plane/pkg/server/config"
	"github.com/envoyproxy/go-control-plane/pkg/server/sotw/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/stream/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
)

// These tests drive an ordered ADS stream (sotw.WithOrderedADS) over a real
// snapshot cache in ADS mode. The stream's Send blocks the way a gRPC Send
// blocks when the peer has stopped reading and the HTTP/2 flow-control window
// is exhausted.
//
// In ordered ADS one goroutine per stream both drains the shared response
// channel into stream.Send and creates watches. The cache pushes a response
// into that channel while it holds its own locks, so a stream that watches more
// type URLs than the channel holds must not be able to stall the cache.

const (
	// stalledNode is the node ID of the stream whose Send blocks, otherNode the
	// ID of a node that has no stream.
	stalledNode = "stalled"
	otherNode   = "other"

	// returnTimeout bounds every wait for a call that is expected to return.
	returnTimeout = 5 * time.Second
)

// versionOnlySnapshot is a cache.ResourceSnapshot that has the same version
// for every type URL and no resources, so every wildcard watch that is behind
// that version receives an empty response.
type versionOnlySnapshot struct {
	version string
}

var _ cache.ResourceSnapshot = versionOnlySnapshot{}

func (s versionOnlySnapshot) GetVersion(string) string { return s.version }

func (versionOnlySnapshot) GetResourcesAndTTL(string) map[string]types.ResourceWithTTL {
	return nil
}

func (versionOnlySnapshot) GetResources(string) map[string]types.Resource { return nil }

func (versionOnlySnapshot) ConstructVersionMap() error { return nil }

func (versionOnlySnapshot) GetVersionMap(string) map[string]string { return nil }

// partialSnapshot is a cache.ResourceSnapshot with no resources and a version
// for the type URLs named in versions only. A watch for any other type URL has
// the empty request version, which equals the snapshot version, so the cache
// keeps the watch open.
type partialSnapshot struct {
	versions map[string]string
}

var _ cache.ResourceSnapshot = partialSnapshot{}

func (s partialSnapshot) GetVersion(typeURL string) string { return s.versions[typeURL] }

func (partialSnapshot) GetResourcesAndTTL(string) map[string]types.ResourceWithTTL {
	return nil
}

func (partialSnapshot) GetResources(string) map[string]types.Resource { return nil }

func (partialSnapshot) ConstructVersionMap() error { return nil }

func (partialSnapshot) GetVersionMap(string) map[string]string { return nil }

type nodeHash struct{}

func (nodeHash) ID(node *core.Node) string { return node.GetId() }

// stalledStream is a gRPC ADS stream whose Send blocks until release is
// closed or the stream context ends.
type stalledStream struct {
	grpc.ServerStream

	ctx     context.Context
	recv    chan *discovery.DiscoveryRequest
	entered chan struct{}                     // closed when the first Send starts
	release chan struct{}                     // closing it lets every Send return
	sent    chan *discovery.DiscoveryResponse // receives the argument of every Send call

	once sync.Once
}

func newStalledStream(ctx context.Context) *stalledStream {
	return &stalledStream{
		ctx:     ctx,
		recv:    make(chan *discovery.DiscoveryRequest, 64),
		entered: make(chan struct{}),
		release: make(chan struct{}),
		sent:    make(chan *discovery.DiscoveryResponse, 256),
	}
}

func (s *stalledStream) Context() context.Context { return s.ctx }

func (s *stalledStream) Send(resp *discovery.DiscoveryResponse) error {
	s.once.Do(func() { close(s.entered) })
	select {
	case s.sent <- resp:
	default:
		return errors.New("test stream recorded too many responses")
	}
	select {
	case <-s.release:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// receive waits for the next n responses passed to Send.
func (s *stalledStream) receive(tb testing.TB, n int) []*discovery.DiscoveryResponse {
	tb.Helper()

	out := make([]*discovery.DiscoveryResponse, 0, n)
	for len(out) < n {
		select {
		case resp := <-s.sent:
			out = append(out, resp)
		case <-time.After(returnTimeout):
			tb.Fatalf("stream sent %d of %d expected responses", len(out), n)
		}
	}
	return out
}

// waitForSend waits until the stream has called Send.
func (s *stalledStream) waitForSend(tb testing.TB) {
	tb.Helper()

	select {
	case <-s.entered:
	case <-time.After(returnTimeout):
		tb.Fatal("stream never called Send")
	}
}

// ack queues the request in which the stalledNode client acknowledges resp.
func (s *stalledStream) ack(resp *discovery.DiscoveryResponse) {
	s.recv <- &discovery.DiscoveryRequest{
		Node:          &core.Node{Id: stalledNode},
		TypeUrl:       resp.GetTypeUrl(),
		VersionInfo:   resp.GetVersionInfo(),
		ResponseNonce: resp.GetNonce(),
	}
}

func (s *stalledStream) Recv() (*discovery.DiscoveryRequest, error) {
	select {
	case req, ok := <-s.recv:
		if !ok {
			return nil, context.Canceled
		}
		return req, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

// returnsWithin reports whether fn returns before returnTimeout.
func returnsWithin(fn func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return true
	case <-time.After(returnTimeout):
		return false
	}
}

// orderedADS is a running ordered ADS stream over a snapshot cache.
type orderedADS struct {
	cache   cache.SnapshotCache
	stream  *stalledStream
	cancel  context.CancelFunc
	handled chan struct{} // closed when the stream handler returns
}

// stop cancels the stream and waits for its handler to return.
func (o *orderedADS) stop(t *testing.T) {
	t.Helper()

	o.cancel()
	select {
	case <-o.handled:
	case <-time.After(returnTimeout):
		t.Error("ADS stream handler did not exit after its context was canceled")
	}
}

// startOrderedADS starts an ordered ADS server over an ADS-mode snapshot cache
// and one stream for stalledNode that requests every type URL in typeURLs. The
// caller stops the stream, or leaves it running when it is meant to be stuck.
func startOrderedADS(t *testing.T, typeURLs []string) *orderedADS {
	t.Helper()

	return startADS(t, typeURLs, sotw.WithOrderedADS())
}

// startADS is startOrderedADS with the server options chosen by the caller.
func startADS(t *testing.T, typeURLs []string, opts ...config.XDSOption) *orderedADS {
	t.Helper()

	sc := cache.NewSnapshotCache(true, nodeHash{}, log.NewTestLogger(t))
	return startADSOver(t, sc, sc, server.CallbackFuncs{}, typeURLs, opts...)
}

// startADSOver starts a server that reads from watcher, which wraps sc, and
// one stream for stalledNode that requests every type URL in typeURLs.
func startADSOver(t *testing.T, sc cache.SnapshotCache, watcher cache.Cache, callbacks server.Callbacks, typeURLs []string, opts ...config.XDSOption) *orderedADS {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	srv := server.NewServer(ctx, watcher, callbacks, opts...)

	str := newStalledStream(ctx)
	node := &core.Node{Id: stalledNode}
	for _, typeURL := range typeURLs {
		str.recv <- &discovery.DiscoveryRequest{Node: node, TypeUrl: typeURL}
	}

	handled := make(chan struct{})
	go func() {
		defer close(handled)
		_ = srv.StreamAggregatedResources(str) // the stream ends with the context
	}()

	require.Eventually(t, func() bool {
		info := sc.GetStatusInfo(stalledNode)
		return info != nil && info.GetNumWatches() == len(typeURLs)
	}, returnTimeout, 5*time.Millisecond, "stream did not create one watch per requested type")

	return &orderedADS{cache: sc, stream: str, cancel: cancel, handled: handled}
}

func knownTypeURLs(t *testing.T) []string {
	t.Helper()

	urls := make([]string, 0, int(types.UnknownType))
	for typ := range types.UnknownType {
		typeURL, err := cache.GetResponseTypeURL(typ)
		require.NoError(t, err)
		urls = append(urls, typeURL)
	}
	return urls
}

func customTypeURLs(n int) []string {
	urls := make([]string, n)
	for i := range urls {
		urls[i] = fmt.Sprintf("type.example.com/custom.Type%02d", i)
	}
	return urls
}

// typeURLsOf returns the type URLs of the responses in the order they were sent.
func typeURLsOf(resps []*discovery.DiscoveryResponse) []string {
	urls := make([]string, len(resps))
	for i, resp := range resps {
		urls[i] = resp.GetTypeUrl()
	}
	return urls
}

// assertCacheNotBlocked checks that calls for otherNode do not wait for the
// stalled stream.
func assertCacheNotBlocked(t *testing.T, sc cache.SnapshotCache, typeURL string) {
	t.Helper()

	assert.True(t, returnsWithin(func() {
		assert.NoError(t, sc.SetSnapshot(t.Context(), otherNode, versionOnlySnapshot{version: "v1"}))
	}), "SetSnapshot for another node blocked")

	assert.True(t, returnsWithin(func() {
		_, err := sc.GetSnapshot(otherNode)
		assert.NoError(t, err)
	}), "GetSnapshot blocked")

	assert.True(t, returnsWithin(func() {
		req := &discovery.DiscoveryRequest{Node: &core.Node{Id: otherNode}, TypeUrl: typeURL}
		_, err := sc.CreateWatch(req, stream.NewSotwSubscription(nil, true), make(chan cache.Response, 1))
		assert.NoError(t, err)
	}), "CreateWatch for another node blocked")
}

// With every known xDS type watched on one ordered ADS stream, a single
// SetSnapshot queues one response per type while Send is blocked. The cache
// does not block, and once Send returns the stream sends the responses in the
// order of the xDS types.
func TestOrderedADSStalledSendKnownTypesDoNotBlockCache(t *testing.T) {
	watched := knownTypeURLs(t)
	ads := startOrderedADS(t, watched)
	t.Cleanup(func() { ads.stop(t) })
	sc, str := ads.cache, ads.stream

	setStalled := func(version string) {
		t.Helper()

		ok := returnsWithin(func() {
			assert.NoError(t, sc.SetSnapshot(t.Context(), stalledNode, versionOnlySnapshot{version: version}))
		})
		require.True(t, ok, "SetSnapshot for the stalled node blocked")
	}

	setStalled("v1")
	str.waitForSend(t)

	// Send is now blocked. Later snapshots find no open watch to respond to.
	for i := 2; i < 50; i++ {
		setStalled(fmt.Sprintf("v%d", i))
	}
	assertCacheNotBlocked(t, sc, watched[0])

	close(str.release)
	resps := str.receive(t, len(watched))
	assert.Equal(t, watched, typeURLsOf(resps))
	for _, resp := range resps {
		assert.Equal(t, "v1", resp.GetVersionInfo())
	}
}

// A stream that watches more type URLs than the shared response channel holds
// initially queues more responses for one SetSnapshot than fit in that channel
// when Send is blocked. The cache does not block: SetSnapshot, GetSnapshot and
// CreateWatch for other nodes return. Once Send returns, every watch receives
// its response without another SetSnapshot.
func TestOrderedADSStalledSendBeyondInitialCapacityDoesNotBlockCache(t *testing.T) {
	watched := customTypeURLs(int(types.UnknownType) + 4)
	ads := startOrderedADS(t, watched)
	t.Cleanup(func() { ads.stop(t) })
	sc, str := ads.cache, ads.stream

	// The context outlives the test: only the stream may release the cache.
	setDone := make(chan error, 1)
	go func() {
		setDone <- sc.SetSnapshot(context.Background(), stalledNode, versionOnlySnapshot{version: "v1"})
	}()
	str.waitForSend(t)

	select {
	case err := <-setDone:
		require.NoError(t, err)
	case <-time.After(returnTimeout):
		t.Fatal("SetSnapshot for the stalled node blocked")
	}
	assertCacheNotBlocked(t, sc, watched[0])

	close(str.release)
	resps := str.receive(t, len(watched))
	assert.ElementsMatch(t, watched, typeURLsOf(resps))
	for _, resp := range resps {
		assert.Equal(t, "v1", resp.GetVersionInfo())
	}
}

// Without the ordered option each type URL has its own response channel with
// room for the single response of its single watch, so the same number of
// watches and the same stalled Send do not block the cache.
func TestUnorderedADSStalledSendDoesNotBlockCache(t *testing.T) {
	watched := customTypeURLs(int(types.UnknownType) + 4)
	ads := startADS(t, watched)
	t.Cleanup(func() { ads.stop(t) })
	sc, str := ads.cache, ads.stream

	ok := returnsWithin(func() {
		assert.NoError(t, sc.SetSnapshot(context.Background(), stalledNode, versionOnlySnapshot{version: "v1"}))
	})
	require.True(t, ok, "SetSnapshot for the stalled node blocked")
	str.waitForSend(t)

	assertCacheNotBlocked(t, sc, watched[0])
}

// After Send returns, a stalled stream that missed a snapshot converges on the
// latest snapshot when the client acknowledges the responses it received: the
// new watches are answered from the current snapshot with no further
// SetSnapshot.
func TestOrderedADSStalledStreamReceivesLatestSnapshotAfterAck(t *testing.T) {
	watched := customTypeURLs(int(types.UnknownType) + 4)
	ads := startOrderedADS(t, watched)
	t.Cleanup(func() { ads.stop(t) })
	sc, str := ads.cache, ads.stream

	go func() {
		assert.NoError(t, sc.SetSnapshot(context.Background(), stalledNode, versionOnlySnapshot{version: "v1"}))
	}()
	str.waitForSend(t)

	// No watch is open: the stream has not acknowledged v1.
	require.Eventually(t, func() bool { return sc.GetStatusInfo(stalledNode).GetNumWatches() == 0 },
		returnTimeout, time.Millisecond)
	require.NoError(t, sc.SetSnapshot(t.Context(), stalledNode, versionOnlySnapshot{version: "v2"}))

	close(str.release)
	for _, resp := range str.receive(t, len(watched)) {
		assert.Equal(t, "v1", resp.GetVersionInfo())
		str.ack(resp)
	}

	resps := str.receive(t, len(watched))
	assert.ElementsMatch(t, watched, typeURLsOf(resps))
	for _, resp := range resps {
		assert.Equal(t, "v2", resp.GetVersionInfo())
	}
}

// requestGate holds the stream handler in the OnStreamRequest callback of the
// request for one type URL, which is before the handler creates its watch.
type requestGate struct {
	typeURL string
	entered chan struct{} // closed when the handler is held
	proceed chan struct{} // closing it releases the handler
	once    sync.Once
}

func newRequestGate(t *testing.T, typeURL string) *requestGate {
	t.Helper()

	g := &requestGate{typeURL: typeURL, entered: make(chan struct{}), proceed: make(chan struct{})}
	t.Cleanup(g.open)
	return g
}

func (g *requestGate) callbacks() server.Callbacks {
	return server.CallbackFuncs{StreamRequestFunc: func(_ int64, req *discovery.DiscoveryRequest) error {
		if req.GetTypeUrl() == g.typeURL {
			close(g.entered)
			<-g.proceed
		}
		return nil
	}}
}

// open releases the handler.
func (g *requestGate) open() { g.once.Do(func() { close(g.proceed) }) }

// waitHeld waits until the handler is held.
func (g *requestGate) waitHeld(t *testing.T) {
	t.Helper()

	select {
	case <-g.entered:
	case <-time.After(returnTimeout):
		t.Fatal("stream handler did not reach the request")
	}
}

func newRequest(typeURL string) *discovery.DiscoveryRequest {
	return &discovery.DiscoveryRequest{Node: &core.Node{Id: stalledNode}, TypeUrl: typeURL}
}

// The stream handler reads a request for a type URL beyond the response channel
// capacity while the channel holds one response for each of the watched type
// URLs: creating the watch answers at once, and the channel is full at that
// point unless the stream moves to a wider one first.
func TestOrderedADSCreateWatchDoesNotWaitForFullChannel(t *testing.T) {
	const extra = "type.example.com/custom.Extra"

	watched := customTypeURLs(int(types.UnknownType))
	gate := newRequestGate(t, extra)
	sc := cache.NewSnapshotCache(true, nodeHash{}, log.NewTestLogger(t))
	ads := startADSOver(t, sc, sc, gate.callbacks(), watched, sotw.WithOrderedADS())
	t.Cleanup(func() { ads.stop(t) })
	str := ads.stream

	// Send returns at once. The gate holds the handler, so nothing drains the
	// channel.
	close(str.release)
	str.recv <- newRequest(extra)
	gate.waitHeld(t)

	versions := map[string]string{extra: "v1"}
	for _, typeURL := range watched {
		versions[typeURL] = "v1"
	}
	require.True(t, returnsWithin(func() {
		assert.NoError(t, sc.SetSnapshot(t.Context(), stalledNode, partialSnapshot{versions: versions}))
	}), "SetSnapshot blocked")
	gate.open()

	resps := str.receive(t, len(watched)+1)
	assert.Equal(t, extra, resps[len(resps)-1].GetTypeUrl())
	assert.ElementsMatch(t, watched, typeURLsOf(resps[:len(watched)]))
	assertCacheNotBlocked(t, sc, watched[0])
}

// A request for a type URL beyond the response channel capacity arrives when
// the stream watches as many type URLs as the channel holds. The stream moves
// to a wider channel. The responses that were sent or queued before are
// delivered once and in order, the request is answered from the current
// snapshot, and the watches that were still open answer the next snapshot.
func TestOrderedADSRequestBeyondChannelCapacity(t *testing.T) {
	const extra = "type.example.com/custom.Extra"

	known := knownTypeURLs(t)
	custom := customTypeURLs(int(types.UnknownType))
	tests := []struct {
		name string
		// watched lists the type URLs of the stream. The snapshot answers the
		// answered ones, and the watches for the others stay open.
		watched  []string
		answered []string
		// openInOrder is set when the open type URLs are all known types, which
		// the cache answers in a fixed order.
		openInOrder bool
	}{
		{name: "one widening", watched: known, answered: known[:len(known)/2], openInOrder: true},
		{name: "two widenings", watched: slices.Concat(known, custom), answered: known},
	}
	for _, tt := range tests {
		open := tt.watched[len(tt.answered):]
		for _, queued := range []bool{true, false} {
			name := tt.name + " with received responses"
			if queued {
				name = tt.name + " with queued responses"
			}
			t.Run(name, func(t *testing.T) {
				gate := newRequestGate(t, extra)
				callbacks := server.Callbacks(server.CallbackFuncs{})
				if queued {
					callbacks = gate.callbacks()
				}
				sc := cache.NewSnapshotCache(true, nodeHash{}, log.NewTestLogger(t))
				ads := startADSOver(t, sc, sc, callbacks, tt.watched, sotw.WithOrderedADS())
				t.Cleanup(func() { ads.stop(t) })
				str := ads.stream
				close(str.release)

				versions := map[string]string{extra: "v1"}
				for _, typeURL := range tt.answered {
					versions[typeURL] = "v1"
				}
				setSnapshot := func() {
					t.Helper()

					require.True(t, returnsWithin(func() {
						assert.NoError(t, sc.SetSnapshot(t.Context(), stalledNode, partialSnapshot{versions: versions}))
					}), "SetSnapshot blocked")
				}

				if queued {
					// The handler is held before it creates the watch for the
					// extra type URL, so the responses stay in the channel.
					str.recv <- newRequest(extra)
					gate.waitHeld(t)
					setSnapshot()
					gate.open()
				} else {
					setSnapshot()
					assert.Equal(t, tt.answered, typeURLsOf(str.receive(t, len(tt.answered))))
					str.recv <- newRequest(extra)
				}

				// Each answered type URL is sent once. The extra type URL is
				// last: its watch was created after the snapshot.
				want := append(slices.Clone(tt.answered), extra)
				if queued {
					assert.Equal(t, want, typeURLsOf(str.receive(t, len(want))))
				} else {
					assert.Equal(t, []string{extra}, typeURLsOf(str.receive(t, 1)))
				}

				// The watches that stayed open receive the next snapshot.
				for _, typeURL := range open {
					versions[typeURL] = "v2"
				}
				setSnapshot()
				got := typeURLsOf(str.receive(t, len(open)))
				if tt.openInOrder {
					assert.Equal(t, open, got)
				} else {
					assert.ElementsMatch(t, open, got)
				}
			})
		}
	}
}

// failWatcher is a cache.Cache whose CreateWatch fails for a type URL it has
// already created a watch for.
type failWatcher struct {
	cache.Cache

	err error

	mu   sync.Mutex
	seen map[string]bool
}

func (w *failWatcher) CreateWatch(req *cache.Request, sub cache.Subscription, resp chan cache.Response) (func(), error) {
	w.mu.Lock()
	again := w.seen[req.GetTypeUrl()]
	w.seen[req.GetTypeUrl()] = true
	w.mu.Unlock()

	if again {
		return nil, w.err
	}
	return w.Cache.CreateWatch(req, sub, resp)
}

// The stream ends with the error of the cache when it cannot create a watch on
// the wider channel again. The watch for the request that needs the wider
// channel is not created at all.
func TestOrderedADSWidenFailureEndsStream(t *testing.T) {
	errWatch := errors.New("cannot create watch")
	watched := customTypeURLs(int(types.UnknownType) + 1)

	sc := &failWatcher{
		Cache: cache.NewSnapshotCache(true, nodeHash{}, log.NewTestLogger(t)),
		err:   errWatch,
		seen:  make(map[string]bool),
	}
	srv := server.NewServer(t.Context(), sc, server.CallbackFuncs{}, sotw.WithOrderedADS())

	str := newStalledStream(t.Context())
	for _, typeURL := range watched {
		str.recv <- newRequest(typeURL)
	}

	done := make(chan error, 1)
	go func() { done <- srv.StreamAggregatedResources(str) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, errWatch)
	case <-time.After(returnTimeout):
		t.Fatal("stream did not end after the cache failed to create a watch")
	}
}

// hookWatcher is a cache.Cache that runs hook before the CreateWatch call
// numbered at.
type hookWatcher struct {
	cache.Cache

	at    int32
	hook  func()
	calls atomic.Int32
}

func (w *hookWatcher) CreateWatch(req *cache.Request, sub cache.Subscription, resp chan cache.Response) (func(), error) {
	if w.calls.Add(1) == w.at {
		w.hook()
	}
	return w.Cache.CreateWatch(req, sub, resp)
}

// A snapshot set after the stream canceled its watches to move to a wider
// channel, and before it created them again, answers every type URL once, at
// the new version, in the order of the xDS types.
func TestOrderedADSSnapshotDuringWidenAnswersEachTypeOnce(t *testing.T) {
	const extra, barrier = "type.example.com/custom.Extra", "type.example.com/custom.Barrier"

	watched := knownTypeURLs(t)
	sc := cache.NewSnapshotCache(true, nodeHash{}, log.NewTestLogger(t))
	// The first CreateWatch calls are the initial watches. The next one creates
	// the first watch again, after every watch was canceled.
	hw := &hookWatcher{Cache: sc, at: int32(len(watched)) + 1, hook: func() {
		assert.NoError(t, sc.SetSnapshot(t.Context(), stalledNode, versionOnlySnapshot{version: "v2"}))
	}}
	ads := startADSOver(t, sc, hw, server.CallbackFuncs{}, watched, sotw.WithOrderedADS())
	t.Cleanup(func() { ads.stop(t) })
	str := ads.stream
	close(str.release)

	// The barrier request follows the extra one, so a repeated response to a
	// watched type URL would be sent before the barrier.
	str.recv <- newRequest(extra)
	str.recv <- newRequest(barrier)

	resps := str.receive(t, len(watched)+2)
	assert.Equal(t, slices.Concat(watched, []string{extra, barrier}), typeURLsOf(resps))
	for _, resp := range resps {
		assert.Equal(t, "v2", resp.GetVersionInfo())
	}
}

// cancelHookWatcher is a cache.Cache that runs hook after the first call of the
// cancel function of the watch for hookType, once armed.
type cancelHookWatcher struct {
	cache.Cache

	hookType string
	hook     func()
	armed    atomic.Bool
	once     sync.Once
}

func (w *cancelHookWatcher) CreateWatch(req *cache.Request, sub cache.Subscription, resp chan cache.Response) (func(), error) {
	cancel, err := w.Cache.CreateWatch(req, sub, resp)
	if err != nil || req.GetTypeUrl() != w.hookType {
		return cancel, err
	}
	return func() {
		cancel()
		if w.armed.Load() {
			w.once.Do(w.hook)
		}
	}, nil
}

// A snapshot set while the stream cancels its watches to move to a wider
// channel is answered in the order of the xDS types, wherever in the sequence
// of cancels it lands: the watches that are still registered are the earliest
// types, and the ones created again afterwards are the later types.
func TestOrderedADSSnapshotInsideWidenKeepsTypeOrder(t *testing.T) {
	const extra = "type.example.com/custom.Extra"

	watched := knownTypeURLs(t)
	for _, hookIndex := range []int{0, len(watched) / 2, len(watched) - 1} {
		t.Run(fmt.Sprintf("after cancel %d", hookIndex), func(t *testing.T) {
			sc := cache.NewSnapshotCache(true, nodeHash{}, log.NewTestLogger(t))
			cw := &cancelHookWatcher{Cache: sc, hookType: watched[hookIndex], hook: func() {
				assert.NoError(t, sc.SetSnapshot(t.Context(), stalledNode, versionOnlySnapshot{version: "v2"}))
			}}
			ads := startADSOver(t, sc, cw, server.CallbackFuncs{}, watched, sotw.WithOrderedADS())
			t.Cleanup(func() { ads.stop(t) })
			str := ads.stream
			close(str.release)

			cw.armed.Store(true)
			str.recv <- newRequest(extra)

			resps := str.receive(t, len(watched)+1)
			assert.Equal(t, append(slices.Clone(watched), extra), typeURLsOf(resps))
			for _, resp := range resps {
				assert.Equal(t, "v2", resp.GetVersionInfo())
			}
		})
	}
}
