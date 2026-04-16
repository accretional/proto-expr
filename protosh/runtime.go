// Package protosh implements the Protosh.Run scripting runtime for
// proto-expr. A ScriptDescriptor is a sequence of statements — Import,
// const/mutable Variable, Dispatch, Expression — that read and write
// a register map keyed by name. Run executes them in order.
//
// Dispatch is pluggable: callers register handlers per uri before
// calling Run. This keeps the runtime independent of any particular
// gRPC client and lets the Transform pipeline wire in-process
// handlers (astkit.Transformer, proto-type.Typer, ...) directly.
//
// The S-expression interpreter for ExpressionDescriptor is not yet
// implemented — a script that includes an Expression statement will
// fail with an "expression: not yet implemented" error.
package protosh

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	pb "github.com/accretional/proto-expr"
)

// Handler implements a single Dispatch uri. It receives the resolved
// request Data and returns a response Data (or an error).
type Handler func(ctx context.Context, req *pb.Data) (*pb.Data, error)

// Runtime executes ScriptDescriptor scripts. Safe to reuse across
// calls; handler registrations persist for the lifetime of the
// Runtime. The register map is per-call.
type Runtime struct {
	pb.UnimplementedProtoshServer
	handlers map[string]Handler
}

// New returns an empty Runtime with no handlers registered.
func New() *Runtime {
	return &Runtime{handlers: map[string]Handler{}}
}

// Register installs a handler for the given uri. The uri matches
// DispatchDescriptor.uri exactly — no scheme parsing or prefix
// matching. Re-registering replaces the previous handler.
func (r *Runtime) Register(uri string, h Handler) {
	r.handlers[uri] = h
}

// Run executes the script's statements in order and returns the last
// written Data. The returned Data is whatever the final statement
// produced: an imported blob, a variable literal, or a dispatch
// response. An empty script returns an empty Data, not nil.
//
// Errors are wrapped with "statement N: ..." so callers can locate
// the failing statement.
func (r *Runtime) Run(ctx context.Context, script *pb.ScriptDescriptor) (*pb.Data, error) {
	if script == nil {
		return nil, errors.New("nil script")
	}
	regs := map[string]*pb.Data{}
	var last *pb.Data = &pb.Data{}

	for i, stmt := range script.GetStatements() {
		var err error
		switch k := stmt.GetKind().(type) {
		case *pb.StatementDescriptor_Imports:
			last, err = r.execImport(k.Imports, regs)
		case *pb.StatementDescriptor_ConstVar:
			last, err = r.execVar(k.ConstVar, regs)
		case *pb.StatementDescriptor_MutableVar:
			last, err = r.execVar(k.MutableVar, regs)
		case *pb.StatementDescriptor_Dispatch:
			last, err = r.execDispatch(ctx, k.Dispatch, regs)
		case *pb.StatementDescriptor_Expression:
			err = errors.New("expression: not yet implemented")
		case nil:
			err = errors.New("empty statement (kind unset)")
		default:
			err = fmt.Errorf("unknown statement kind %T", k)
		}
		if err != nil {
			return nil, fmt.Errorf("statement %d: %w", i, err)
		}
	}
	return last, nil
}

func (r *Runtime) execImport(imp *pb.ImportDescriptor, regs map[string]*pb.Data) (*pb.Data, error) {
	if imp.GetName() == "" {
		return nil, errors.New("import: name required")
	}
	data, err := readURI(imp.GetUri())
	if err != nil {
		return nil, fmt.Errorf("import %q: %w", imp.GetName(), err)
	}
	regs[imp.GetName()] = data
	return data, nil
}

func (r *Runtime) execVar(v *pb.VariableDescriptor, regs map[string]*pb.Data) (*pb.Data, error) {
	if v.GetName() == "" {
		return nil, errors.New("variable: name required")
	}
	d := v.GetData()
	if d == nil {
		d = &pb.Data{}
	}
	regs[v.GetName()] = d
	return d, nil
}

func (r *Runtime) execDispatch(ctx context.Context, d *pb.DispatchDescriptor, regs map[string]*pb.Data) (*pb.Data, error) {
	if d.GetUri() == "" {
		return nil, errors.New("dispatch: uri required")
	}
	h, ok := r.handlers[d.GetUri()]
	if !ok {
		return nil, fmt.Errorf("dispatch: no handler registered for %q", d.GetUri())
	}
	req := resolveRequest(d.GetRequest(), regs)
	resp, err := h(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("dispatch %q: %w", d.GetUri(), err)
	}
	if resp == nil {
		resp = &pb.Data{}
	}
	switch dest := d.GetDest().(type) {
	case *pb.DispatchDescriptor_Name:
		if dest.Name == "" {
			return nil, errors.New("dispatch: dest name empty")
		}
		regs[dest.Name] = resp
	case *pb.DispatchDescriptor_Path:
		if dest.Path == "" {
			return nil, errors.New("dispatch: dest path empty")
		}
		if err := writePath(dest.Path, resp); err != nil {
			return nil, fmt.Errorf("dispatch write %q: %w", dest.Path, err)
		}
	case nil:
		// No destination — caller relies on the returned Data only.
	default:
		return nil, fmt.Errorf("dispatch: unknown dest type %T", dest)
	}
	return resp, nil
}

// resolveRequest applies the "text is a register reference" rule:
// if req.encoding is text and the text names an existing register,
// the register's value replaces req. Otherwise req is returned as-is.
func resolveRequest(req *pb.Data, regs map[string]*pb.Data) *pb.Data {
	if req == nil {
		return &pb.Data{}
	}
	t, ok := req.GetEncoding().(*pb.Data_Text)
	if !ok {
		return req
	}
	if reg, present := regs[t.Text]; present {
		return reg
	}
	return req
}

// readURI reads a URI into a Data. Supported schemes: file (default
// when no scheme is given). The file's bytes become Data.binary; the
// Data.type field is left empty — callers that need type metadata
// should set it via a subsequent const_var.
func readURI(uri string) (*pb.Data, error) {
	if uri == "" {
		return nil, errors.New("uri required")
	}
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("parse uri: %w", err)
	}
	scheme := u.Scheme
	var path string
	switch scheme {
	case "", "file":
		// file:///abs/path or bare path.
		if u.Scheme == "" {
			path = uri
		} else {
			path = u.Path
		}
	default:
		return nil, fmt.Errorf("unsupported scheme %q", scheme)
	}
	bs, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &pb.Data{Encoding: &pb.Data_Binary{Binary: bs}}, nil
}

// writePath writes d's payload (binary if present, else text) to
// path. Directories are created as needed.
func writePath(path string, d *pb.Data) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var bs []byte
	switch enc := d.GetEncoding().(type) {
	case *pb.Data_Binary:
		bs = enc.Binary
	case *pb.Data_Text:
		bs = []byte(enc.Text)
	case nil:
		bs = nil
	default:
		return fmt.Errorf("unknown encoding %T", enc)
	}
	return os.WriteFile(path, bs, 0o644)
}
