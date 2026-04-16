package protosh_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/accretional/proto-expr"
	"github.com/accretional/proto-expr/protosh"
)

// startProtosh spins up a Protosh server over bufconn and returns a
// client plus the runtime (for handler registration) and teardown.
func startProtosh(t *testing.T) (pb.ProtoshClient, *protosh.Runtime, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	runtime := protosh.New()
	pb.RegisterProtoshServer(srv, runtime)

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("server exited: %v", err)
		}
	}()

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return pb.NewProtoshClient(conn), runtime, func() {
		conn.Close()
		srv.Stop()
		lis.Close()
	}
}

func TestProtoshE2E_EmptyScript(t *testing.T) {
	c, _, teardown := startProtosh(t)
	defer teardown()
	out, err := c.Run(context.Background(), &pb.ScriptDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("nil Data")
	}
}

func TestProtoshE2E_DispatchPipeline(t *testing.T) {
	c, r, teardown := startProtosh(t)
	defer teardown()
	r.Register("upper", func(_ context.Context, req *pb.Data) (*pb.Data, error) {
		return &pb.Data{Encoding: &pb.Data_Text{Text: strings.ToUpper(req.GetText())}}, nil
	})

	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{
			{Kind: &pb.StatementDescriptor_ConstVar{
				ConstVar: &pb.VariableDescriptor{Name: "in", Data: &pb.Data{Encoding: &pb.Data_Text{Text: "hello world"}}},
			}},
			{Kind: &pb.StatementDescriptor_Dispatch{
				Dispatch: &pb.DispatchDescriptor{
					Uri:     "upper",
					Request: &pb.Data{Encoding: &pb.Data_Text{Text: "in"}},
					Dest:    &pb.DispatchDescriptor_Name{Name: "out"},
				},
			}},
		},
	}
	out, err := c.Run(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetText() != "HELLO WORLD" {
		t.Errorf("got %q", out.GetText())
	}
}

func TestProtoshE2E_MissingHandlerError(t *testing.T) {
	c, _, teardown := startProtosh(t)
	defer teardown()
	script := &pb.ScriptDescriptor{
		Statements: []*pb.StatementDescriptor{{
			Kind: &pb.StatementDescriptor_Dispatch{
				Dispatch: &pb.DispatchDescriptor{Uri: "nope", Dest: &pb.DispatchDescriptor_Name{Name: "x"}},
			},
		}},
	}
	_, err := c.Run(context.Background(), script)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no handler") {
		t.Errorf("unexpected error: %v", err)
	}
}
