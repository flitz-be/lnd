//go:build dev
// +build dev

package devrpc

import (
	"context"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/routing"
	"google.golang.org/grpc"
	"gopkg.in/macaroon-bakery.v2/bakery"
	"sync/atomic"
)

const (
	// subServerName is the name of the sub rpc server. We'll use this name
	// to register ourselves, and we also require that the main
	// SubServerConfigDispatcher instance recognize tt as the name of our
	// RPC service.
	subServerName = "DevRPC"
)

var (
	// macPermissions maps RPC calls to the permissions they require.
	macPermissions = map[string][]bakery.Op{
		"/devrpc.Dev/ImportGraph": {{
			Entity: "offchain",
			Action: "write",
		}},
	}
)

// ServerShell is a shell struct holding a reference to the actual sub-server.
// It is used to register the gRPC sub-server with the root server before we
// have the necessary dependencies to populate the actual sub-server.
type ServerShell struct {
	DevServer
}

// Server is a sub-server of the main RPC server: the dev RPC. This sub
// RPC server allows developers to set and query LND state that is not possible
// during normal operation.
type Server struct {
	started  int32 // To be used atomically.
	shutdown int32 // To be used atomically.

	// Required by the grpc-gateway/v2 library for forward compatibility.
	// Must be after the atomically used variables to not break struct
	// alignment.
	UnimplementedDevServer

	cfg *Config
}

// A compile time check to ensure that Server fully implements the
// DevServer gRPC service.
var _ DevServer = (*Server)(nil)

// New returns a new instance of the devrpc Dev sub-server. We also return the
// set of permissions for the macaroons that we may create within this method.
// If the macaroons we need aren't found in the filepath, then we'll create them
// on start up. If we're unable to locate, or create the macaroons we need, then
// we'll return with an error.
func New(cfg *Config) (*Server, lnrpc.MacaroonPerms, error) {
	// We don't create any new macaroons for this subserver, instead reuse
	// existing onchain/offchain permissions.
	server := &Server{
		cfg: cfg,
	}

	return server, macPermissions, nil
}

// Start launches any helper goroutines required for the Server to function.
//
// NOTE: This is part of the lnrpc.SubServer interface.
func (s *Server) Start() error {
	if atomic.AddInt32(&s.started, 1) != 1 {
		return nil
	}

	return nil
}

// Stop signals any active goroutines for a graceful closure.
//
// NOTE: This is part of the lnrpc.SubServer interface.
func (s *Server) Stop() error {
	if atomic.AddInt32(&s.shutdown, 1) != 1 {
		return nil
	}

	return nil
}

// Name returns a unique string representation of the sub-server. This can be
// used to identify the sub-server and also de-duplicate them.
//
// NOTE: This is part of the lnrpc.SubServer interface.
func (s *Server) Name() string {
	return subServerName
}

// RegisterWithRootServer will be called by the root gRPC server to direct a
// sub RPC server to register itself with the main gRPC root server. Until this
// is called, each sub-server won't be able to have
// requests routed towards it.
//
// NOTE: This is part of the lnrpc.GrpcHandler interface.
func (r *ServerShell) RegisterWithRootServer(grpcServer *grpc.Server) error {
	// We make sure that we register it with the main gRPC server to ensure
	// all our methods are routed properly.
	RegisterDevServer(grpcServer, r)

	log.Debugf("DEV RPC server successfully register with root the " +
		"gRPC server")

	return nil
}

// RegisterWithRestServer will be called by the root REST mux to direct a sub
// RPC server to register itself with the main REST mux server. Until this is
// called, each sub-server won't be able to have requests routed towards it.
//
// NOTE: This is part of the lnrpc.GrpcHandler interface.
func (r *ServerShell) RegisterWithRestServer(ctx context.Context,
	mux *runtime.ServeMux, dest string, opts []grpc.DialOption) error {

	// We make sure that we register it with the main REST server to ensure
	// all our methods are routed properly.
	err := RegisterDevHandlerFromEndpoint(ctx, mux, dest, opts)
	if err != nil {
		log.Errorf("Could not register DEV REST server with the root "+
			"REST server: %v", err)
		return err
	}

	log.Debugf("DEV REST server successfully registered with the root " +
		"REST server")
	return nil
}

// CreateSubServer populates the subserver's dependencies using the passed
// SubServerConfigDispatcher. This method should fully initialize the
// sub-server instance, making it ready for action. It returns the macaroon
// permissions that the sub-server wishes to pass on to the root server for all
// methods routed towards it.
//
// NOTE: This is part of the lnrpc.GrpcHandler interface.
func (r *ServerShell) CreateSubServer(configRegistry lnrpc.SubServerConfigDispatcher) (
	lnrpc.SubServer, lnrpc.MacaroonPerms, error) {

	subServer, macPermissions, err := createNewSubServer(configRegistry)
	if err != nil {
		return nil, nil, err
	}

	r.DevServer = subServer
	return subServer, macPermissions, nil
}

// ImportGraph imports a graph dump (without auth proofs).
//
// NOTE: Part of the DevServer interface.
func (s *Server) ImportGraph(ctx context.Context,
	graph *lnrpc.ChannelGraph) (*ImportGraphResponse, error) {

	// Obtain the pointer to the global singleton channel graph.
	err := routing.ImportGraphData(
		graph, s.cfg.GraphDB, *s.cfg.ActiveNetParams.GenesisHash,
	)
	if err != nil {
		return nil, err
	}

	return &ImportGraphResponse{}, nil
}
