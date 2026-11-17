package executor

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/fullstorydev/grpcurl"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/model"
)

// GRPC invokes one RPC. The service description comes from .proto files in
// the application's assets when the step names any, otherwise from server
// reflection. A non-OK status is a normal result, not an error, so negative
// tests can assert on it.
func GRPC(ctx context.Context, s *model.GRPCStep, creds *auth.Credentials, protosDir string) (*model.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout(s.TimeoutMs, 30000))
	defer cancel()

	var tc credentials.TransportCredentials
	if s.Plaintext {
		tc = insecure.NewCredentials()
	} else if creds.TLS != nil {
		tc = credentials.NewTLS(creds.TLS.Clone())
	} else {
		tc = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	cc, err := grpc.NewClient(s.Target, grpc.WithTransportCredentials(tc))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", s.Target, err)
	}
	defer cc.Close()

	var headers []string
	for k, v := range creds.Headers {
		headers = append(headers, strings.ToLower(k)+": "+v)
	}
	for k, v := range s.Metadata {
		if v != "" {
			headers = append(headers, strings.ToLower(k)+": "+v)
		}
	}

	var src grpcurl.DescriptorSource
	if len(s.ProtoFiles) > 0 {
		for _, f := range s.ProtoFiles {
			if !model.ValidFileName(f) {
				return nil, fmt.Errorf("proto file %q must be a plain file name in assets/protos", f)
			}
		}
		src, err = grpcurl.DescriptorSourceFromProtoFiles([]string{filepath.Clean(protosDir)}, s.ProtoFiles...)
		if err != nil {
			return nil, fmt.Errorf("load proto files: %w", err)
		}
	} else {
		refClient := grpcreflect.NewClientAuto(ctx, cc)
		defer refClient.Reset()
		src = grpcurl.DescriptorSourceFromServer(ctx, refClient)
	}

	msg := "{}"
	if s.Message != nil {
		if str, ok := s.Message.(string); ok {
			msg = str
		} else {
			msg = mustJSON(s.Message)
		}
	}
	parser, formatter, err := grpcurl.RequestParserAndFormatter(grpcurl.FormatJSON, src, strings.NewReader(msg), grpcurl.FormatOptions{EmitJSONDefaultFields: true})
	if err != nil {
		return nil, fmt.Errorf("prepare request: %w", err)
	}
	var out bytes.Buffer
	h := &grpcurl.DefaultEventHandler{Out: &out, Formatter: formatter}
	start := time.Now()
	if err := grpcurl.InvokeRPC(ctx, src, cc, s.Method, headers, h, parser.Next); err != nil {
		return nil, fmt.Errorf("invoke %s: %w", s.Method, err)
	}
	resp := &model.Response{DurationMs: time.Since(start).Milliseconds()}
	// A nil status means the call completed with OK.
	st := h.Status
	resp.Status = int(st.Code())
	resp.StatusText = codeName(st.Code().String())
	if resp.Status != 0 {
		resp.Body = st.Message()
		resp.JSON = mustJSON(map[string]any{"code": resp.StatusText, "message": st.Message()})
		return resp, nil
	}
	// The handler writes each response message as its own JSON document.
	var msgs []json.RawMessage
	dec := json.NewDecoder(&out)
	for {
		var m json.RawMessage
		if err := dec.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		msgs = append(msgs, m)
	}
	switch len(msgs) {
	case 0:
		resp.JSON = "{}"
	case 1:
		resp.JSON = string(msgs[0])
	default:
		resp.JSON = mustJSON(map[string]any{"responses": msgs})
	}
	resp.Body = resp.JSON
	return resp, nil
}

// codeName turns grpc's "NotFound" into the canonical "NOT_FOUND".
func codeName(s string) string {
	if s == "OK" {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
}
