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
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/testplane/testplane/internal/auth"
	"github.com/testplane/testplane/internal/model"
)

// grpcSource connects to the target and finds the service description:
// .proto files when the step names any, otherwise server reflection.
func grpcSource(ctx context.Context, s *model.GRPCStep, creds *auth.Credentials, protosDir string) (*grpc.ClientConn, grpcurl.DescriptorSource, []string, func(), error) {
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
		return nil, nil, nil, nil, fmt.Errorf("dial %s: %w", s.Target, err)
	}
	closers := []func(){func() { cc.Close() }}
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}

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
				closeAll()
				return nil, nil, nil, nil, fmt.Errorf("proto file %q must be a plain file name in assets/protos", f)
			}
		}
		src, err = grpcurl.DescriptorSourceFromProtoFiles([]string{filepath.Clean(protosDir)}, s.ProtoFiles...)
		if err != nil {
			closeAll()
			return nil, nil, nil, nil, fmt.Errorf("load proto files: %w", err)
		}
	} else {
		refClient := grpcreflect.NewClientAuto(ctx, cc)
		closers = append(closers, refClient.Reset)
		src = grpcurl.DescriptorSourceFromServer(ctx, refClient)
	}

	return cc, src, headers, closeAll, nil
}

// GRPCMethod describes one RPC for the method picker.
type GRPCMethod struct {
	Name            string `json:"name"`     // Check
	FullName        string `json:"fullName"` // grpc.health.v1.Health/Check
	InputType       string `json:"inputType"`
	OutputType      string `json:"outputType"`
	ClientStreaming bool   `json:"clientStreaming,omitempty"`
	ServerStreaming bool   `json:"serverStreaming,omitempty"`
	Template        any    `json:"template"` // an example request with every field present
}

// GRPCService is one service and its methods.
type GRPCService struct {
	Name    string       `json:"name"`
	Methods []GRPCMethod `json:"methods"`
}

// GRPCDescribe lists the services and methods a target offers, with a
// request template for each method.
func GRPCDescribe(ctx context.Context, s *model.GRPCStep, creds *auth.Credentials, protosDir string) ([]GRPCService, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout(s.TimeoutMs, 20000))
	defer cancel()
	_, src, _, closeAll, err := grpcSource(ctx, s, creds, protosDir)
	if err != nil {
		return nil, err
	}
	defer closeAll()
	names, err := grpcurl.ListServices(src)
	if err != nil {
		return nil, fmt.Errorf("list services of %s: %w", s.Target, err)
	}
	_, formatter, err := grpcurl.RequestParserAndFormatter(grpcurl.FormatJSON, src, strings.NewReader(""), grpcurl.FormatOptions{EmitJSONDefaultFields: true})
	if err != nil {
		return nil, err
	}
	out := []GRPCService{}
	for _, name := range names {
		// The reflection service describes itself; nobody tests it.
		if strings.HasPrefix(name, "grpc.reflection.") {
			continue
		}
		d, err := src.FindSymbol(name)
		if err != nil {
			return nil, fmt.Errorf("describe %s: %w", name, err)
		}
		sd, ok := d.(*desc.ServiceDescriptor)
		if !ok {
			continue
		}
		svc := GRPCService{Name: name, Methods: []GRPCMethod{}}
		for _, m := range sd.GetMethods() {
			gm := GRPCMethod{Name: m.GetName(), FullName: name + "/" + m.GetName(), InputType: m.GetInputType().GetFullyQualifiedName(), OutputType: m.GetOutputType().GetFullyQualifiedName(),
				ClientStreaming: m.IsClientStreaming(), ServerStreaming: m.IsServerStreaming(), Template: map[string]any{}}
			if text, err := formatter(grpcurl.MakeTemplate(m.GetInputType())); err == nil {
				var tpl any
				if json.Unmarshal([]byte(text), &tpl) == nil {
					gm.Template = tpl
				}
			}
			svc.Methods = append(svc.Methods, gm)
		}
		out = append(out, svc)
	}
	return out, nil
}

// GRPC invokes one RPC. The service description comes from .proto files in
// the application's assets when the step names any, otherwise from server
// reflection. A non-OK status is a normal result, not an error, so negative
// tests can assert on it.
func GRPC(ctx context.Context, s *model.GRPCStep, creds *auth.Credentials, protosDir string) (*model.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout(s.TimeoutMs, 30000))
	defer cancel()

	cc, src, headers, closeAll, err := grpcSource(ctx, s, creds, protosDir)
	if err != nil {
		return nil, err
	}
	defer closeAll()

	msg := "{}"
	if s.Message != nil {
		switch m := s.Message.(type) {
		case string:
			msg = m
		case []any:
			// A list sends one message per element (client streaming).
			parts := make([]string, len(m))
			for i, item := range m {
				parts[i] = mustJSON(item)
			}
			msg = strings.Join(parts, "\n")
		default:
			msg = mustJSON(m)
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
