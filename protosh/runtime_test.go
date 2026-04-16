package protosh_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/accretional/proto-expr"
	"github.com/accretional/proto-expr/protosh"
)

func TestRun_EmptyScript(t *testing.T) {
	out, err := protosh.New().Run(context.Background(), &pb.ScriptDescriptor{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out == nil {
		t.Fatal("expected empty Data, got nil")
	}
}

func TestRun_NilScript(t *testing.T) {
	if _, err := protosh.New().Run(context.Background(), nil); err == nil {
		t.Error("expected error")
	}
}

func TestRun_ImportReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := protosh.New()
	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{{
			Kind: &pb.StatementDescriptor_Imports{
				Imports: &pb.ImportDescriptor{Name: "blob", Uri: "file://" + path},
			},
		}},
	}
	out, err := r.Run(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	bin := out.GetBinary()
	if string(bin) != "hello" {
		t.Errorf("binary: got %q, want %q", bin, "hello")
	}
}

func TestRun_ImportBarePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(path, []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{{
			Kind: &pb.StatementDescriptor_Imports{
				Imports: &pb.ImportDescriptor{Name: "x", Uri: path},
			},
		}},
	}
	out, err := protosh.New().Run(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	if string(out.GetBinary()) != "world" {
		t.Errorf("got %q", out.GetBinary())
	}
}

func TestRun_ImportMissingFileFails(t *testing.T) {
	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{{
			Kind: &pb.StatementDescriptor_Imports{
				Imports: &pb.ImportDescriptor{Name: "x", Uri: "file:///does-not-exist-xyz"},
			},
		}},
	}
	_, err := protosh.New().Run(context.Background(), script)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "statement 0") {
		t.Errorf("error missing statement index: %v", err)
	}
}

func TestRun_ConstVarStored(t *testing.T) {
	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{
			{Kind: &pb.StatementDescriptor_ConstVar{
				ConstVar: &pb.VariableDescriptor{Name: "greeting", Data: &pb.Data{Encoding: &pb.Data_Text{Text: "hi"}}},
			}},
		},
	}
	out, err := protosh.New().Run(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetText() != "hi" {
		t.Errorf("got %q, want %q", out.GetText(), "hi")
	}
}

func TestRun_DispatchWithRegisterRequest(t *testing.T) {
	r := protosh.New()
	// Handler uppercases text input.
	r.Register("local://upper", func(_ context.Context, req *pb.Data) (*pb.Data, error) {
		return &pb.Data{Encoding: &pb.Data_Text{Text: strings.ToUpper(req.GetText())}}, nil
	})

	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{
			{Kind: &pb.StatementDescriptor_ConstVar{
				ConstVar: &pb.VariableDescriptor{
					Name: "name",
					Data: &pb.Data{Encoding: &pb.Data_Text{Text: "hello"}},
				},
			}},
			{Kind: &pb.StatementDescriptor_Dispatch{
				Dispatch: &pb.DispatchDescriptor{
					Uri:     "local://upper",
					Request: &pb.Data{Encoding: &pb.Data_Text{Text: "name"}}, // register reference
					Dest:    &pb.DispatchDescriptor_Name{Name: "shout"},
				},
			}},
		},
	}
	out, err := r.Run(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.GetText(); got != "HELLO" {
		t.Errorf("got %q, want HELLO", got)
	}
}

func TestRun_DispatchLiteralRequestFallthrough(t *testing.T) {
	r := protosh.New()
	r.Register("echo", func(_ context.Context, req *pb.Data) (*pb.Data, error) {
		return req, nil
	})
	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{
			// Text "xyz" is not a register, so it's passed literally.
			{Kind: &pb.StatementDescriptor_Dispatch{
				Dispatch: &pb.DispatchDescriptor{
					Uri:     "echo",
					Request: &pb.Data{Encoding: &pb.Data_Text{Text: "xyz"}},
					Dest:    &pb.DispatchDescriptor_Name{Name: "out"},
				},
			}},
		},
	}
	out, err := r.Run(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetText() != "xyz" {
		t.Errorf("got %q", out.GetText())
	}
}

func TestRun_DispatchWritesPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")

	r := protosh.New()
	r.Register("make", func(_ context.Context, _ *pb.Data) (*pb.Data, error) {
		return &pb.Data{Encoding: &pb.Data_Binary{Binary: []byte{0x01, 0x02, 0x03}}}, nil
	})
	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{
			{Kind: &pb.StatementDescriptor_Dispatch{
				Dispatch: &pb.DispatchDescriptor{
					Uri:  "make",
					Dest: &pb.DispatchDescriptor_Path{Path: path},
				},
			}},
		},
	}
	if _, err := r.Run(context.Background(), script); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string([]byte{1, 2, 3}) {
		t.Errorf("written bytes: got %v", got)
	}
}

func TestRun_DispatchMissingHandler(t *testing.T) {
	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{{
			Kind: &pb.StatementDescriptor_Dispatch{
				Dispatch: &pb.DispatchDescriptor{Uri: "nope", Dest: &pb.DispatchDescriptor_Name{Name: "x"}},
			},
		}},
	}
	_, err := protosh.New().Run(context.Background(), script)
	if err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Errorf("expected no-handler error, got %v", err)
	}
}

func TestRun_DispatchHandlerError(t *testing.T) {
	r := protosh.New()
	sentinel := errors.New("boom")
	r.Register("fail", func(context.Context, *pb.Data) (*pb.Data, error) { return nil, sentinel })
	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{{
			Kind: &pb.StatementDescriptor_Dispatch{
				Dispatch: &pb.DispatchDescriptor{Uri: "fail", Dest: &pb.DispatchDescriptor_Name{Name: "x"}},
			},
		}},
	}
	_, err := r.Run(context.Background(), script)
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

func TestRun_ChainedDispatch(t *testing.T) {
	// pipeline: import blob -> uppercase -> append "!"
	dir := t.TempDir()
	path := filepath.Join(dir, "seed.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := protosh.New()
	r.Register("upper", func(_ context.Context, req *pb.Data) (*pb.Data, error) {
		text := string(req.GetBinary())
		if req.GetText() != "" {
			text = req.GetText()
		}
		return &pb.Data{Encoding: &pb.Data_Text{Text: strings.ToUpper(text)}}, nil
	})
	r.Register("bang", func(_ context.Context, req *pb.Data) (*pb.Data, error) {
		return &pb.Data{Encoding: &pb.Data_Text{Text: req.GetText() + "!"}}, nil
	})

	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{
			{Kind: &pb.StatementDescriptor_Imports{
				Imports: &pb.ImportDescriptor{Name: "seed", Uri: path},
			}},
			{Kind: &pb.StatementDescriptor_Dispatch{
				Dispatch: &pb.DispatchDescriptor{
					Uri:     "upper",
					Request: &pb.Data{Encoding: &pb.Data_Text{Text: "seed"}},
					Dest:    &pb.DispatchDescriptor_Name{Name: "seed"},
				},
			}},
			{Kind: &pb.StatementDescriptor_Dispatch{
				Dispatch: &pb.DispatchDescriptor{
					Uri:     "bang",
					Request: &pb.Data{Encoding: &pb.Data_Text{Text: "seed"}},
					Dest:    &pb.DispatchDescriptor_Name{Name: "seed"},
				},
			}},
		},
	}
	out, err := r.Run(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetText() != "HI!" {
		t.Errorf("got %q, want HI!", out.GetText())
	}
}

func TestRun_ExpressionNotImplemented(t *testing.T) {
	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{{
			Kind: &pb.StatementDescriptor_Expression{
				Expression: &pb.ExpressionDescriptor{Name: "x"},
			},
		}},
	}
	_, err := protosh.New().Run(context.Background(), script)
	if err == nil || !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("expected not-implemented, got %v", err)
	}
}

func TestRun_ImplementsProtoshServer(t *testing.T) {
	// Compile-time check: Runtime satisfies pb.ProtoshServer.
	var _ pb.ProtoshServer = protosh.New()
}
