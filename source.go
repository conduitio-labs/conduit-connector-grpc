// Copyright © 2023 Meroxa, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package grpcserver

//go:generate paramgen -output=paramgen_src.go SourceConfig

import (
	"context"
	"fmt"
	"net"
	"sync"

	pb "github.com/conduitio-labs/conduit-connector-grpc-server/proto/v1"
	"github.com/conduitio-labs/conduit-connector-grpc-server/source"
	sdk "github.com/conduitio/conduit-connector-sdk"
	"google.golang.org/grpc"
)

type Source struct {
	sdk.UnimplementedSource

	config SourceConfig
	server *source.Server

	// for stopping the server
	grpcSrv *grpc.Server
	wg      sync.WaitGroup

	// used only for injecting a listener in tests
	listener net.Listener
}

type SourceConfig struct {
	// url to gRPC server
	URL string `json:"url" validate:"required"`
}

// NewSourceWithListener for testing purposes.
func NewSourceWithListener(lis net.Listener) sdk.Source {
	return sdk.SourceWithMiddleware(&Source{listener: lis}, sdk.DefaultSourceMiddleware()...)
}

func NewSource() sdk.Source {
	return sdk.SourceWithMiddleware(&Source{}, sdk.DefaultSourceMiddleware()...)
}

func (s *Source) Parameters() map[string]sdk.Parameter {
	return s.config.Parameters()
}

func (s *Source) Configure(ctx context.Context, cfg map[string]string) error {
	sdk.Logger(ctx).Info().Msg("Configuring Source...")
	err := sdk.Util.ParseConfig(cfg, &s.config)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	return nil
}

func (s *Source) Open(ctx context.Context, pos sdk.Position) error {
	sdk.Logger(ctx).Info().Msg("Opening Source...")
	s.server = source.NewServer(ctx)
	err := s.runServer(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *Source) Read(ctx context.Context) (sdk.Record, error) {
	select {
	case <-ctx.Done():
		return sdk.Record{}, ctx.Err()
	case record, ok := <-s.server.RecordCh:
		if !ok {
			return sdk.Record{}, fmt.Errorf("record channel is closed")
		}
		return record, nil
	}
}

func (s *Source) Ack(ctx context.Context, position sdk.Position) error {
	sdk.Logger(ctx).Debug().Str("position", string(position)).Msg("got ack")
	return s.server.SendAck(position)
}

func (s *Source) Teardown(ctx context.Context) error {
	if s.server != nil {
		s.server.Close()
	}
	if s.grpcSrv != nil {
		s.grpcSrv.Stop()
		s.wg.Wait()
	}
	return nil
}

func (s *Source) runServer(ctx context.Context) error {
	// listener can be set for test purposes
	if s.listener == nil {
		lis, err := net.Listen("tcp", s.config.URL)
		if err != nil {
			return fmt.Errorf("failed to listen: %w", err)
		}
		s.listener = lis
	}
	s.grpcSrv = grpc.NewServer()
	pb.RegisterSourceServiceServer(s.grpcSrv, s.server)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.grpcSrv.Serve(s.listener); err != nil {
			sdk.Logger(ctx).Error().Msg("failed to serve")
			return
		}
	}()
	return nil
}
