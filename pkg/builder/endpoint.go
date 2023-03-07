package builder

import (
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyendpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// LBEndpointOpt provides a hook to modify a LBEndpoint given pre-defined
// builder methods. This provides the ability to abstract large boilerplate
// while building envoy proto objects. The goal is to provide users of the library
// the ability to write business logic around what needs to be built rather
// than focusing on building the objects.
type LBEndpointOpt func(*envoyendpoint.LbEndpoint)

func WithHealthStatus(hs corev3.HealthStatus) func(*envoyendpoint.LbEndpoint) {
	return func(lbe *envoyendpoint.LbEndpoint) {
		lbe.HealthStatus = hs
	}
}

func WithMetadata(md *corev3.Metadata) func(*envoyendpoint.LbEndpoint) {
	return func(lbe *envoyendpoint.LbEndpoint) {
		lbe.Metadata = md
	}
}

func WithLBWeight(weight int) func(*envoyendpoint.LbEndpoint) {
	return func(lbe *envoyendpoint.LbEndpoint) {
		lbe.LoadBalancingWeight = &wrapperspb.UInt32Value{Value: uint32(weight)}
	}
}

func WithEndpoint(endpoint *envoyendpoint.Endpoint) func(*envoyendpoint.LbEndpoint) {
	return func(lbe *envoyendpoint.LbEndpoint) {
		lbe.HostIdentifier = &envoyendpoint.LbEndpoint_Endpoint{
			Endpoint: endpoint,
		}
	}
}

func WithNamedEndpoint(name string) func(*envoyendpoint.LbEndpoint) {
	return func(lbe *envoyendpoint.LbEndpoint) {
		lbe.HostIdentifier = &envoyendpoint.LbEndpoint_EndpointName{
			EndpointName: name,
		}
	}
}

// NewLocalityEndpoint constructor builds the target LE
// given the provided optional values from the users input.
func NewLbEndpoint(opts ...LBEndpointOpt) *envoyendpoint.LbEndpoint {
	var le envoyendpoint.LbEndpoint

	// Apply our user inputs to the load balanced endpoint.
	for _, opt := range opts {
		opt(&le)
	}

	return &le
}

type EndpointOpt func(*envoyendpoint.Endpoint)

func WithSocketAddress(host string, port int, protocol corev3.SocketAddress_Protocol) func(*envoyendpoint.Endpoint) {
	return func(e *envoyendpoint.Endpoint) {
		e.Address = &corev3.Address{
			Address: &corev3.Address_SocketAddress{
				SocketAddress: &corev3.SocketAddress{
					Protocol: protocol,
					Address:  host,
					PortSpecifier: &corev3.SocketAddress_PortValue{
						PortValue: uint32(port),
					},
				},
			},
		}
	}
}

func WithHostname(hostname string) func(*envoyendpoint.Endpoint) {
	return func(e *envoyendpoint.Endpoint) {
		e.Hostname = hostname
	}
}

func WithActiveHealthCheckOverrides(hostname string, port int, disable bool) func(*envoyendpoint.Endpoint) {
	return func(e *envoyendpoint.Endpoint) {
		e.HealthCheckConfig = &envoyendpoint.Endpoint_HealthCheckConfig{
			DisableActiveHealthCheck: disable,
			Hostname:                 hostname,
			PortValue:                uint32(port),
		}
	}
}

// NewEndpoint constructor builds the target LE
// given the provided optional values from the users input.
func NewEndpoint(opts ...EndpointOpt) *envoyendpoint.Endpoint {
	var e envoyendpoint.Endpoint

	// Apply our user inputs to the load balanced endpoint.
	for _, opt := range opts {
		opt(&e)
	}

	return &e
}
