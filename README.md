# proto-expr

A minimal proto-only scripting language designed to drive RPC pipelines.

## Schema (`expression.proto`)

`ScriptDescriptor` is a sequence of `StatementDescriptor`s. Each
statement is a oneof:

- **`ImportDescriptor { name, uri }`** — read a resource into the
  register named `name`.
- **`VariableDescriptor { name, data }`** — store a literal `Data`
  in a register (`const_var` or `mutable_var`).
- **`DispatchDescriptor { uri, request, dest }`** — invoke an RPC at
  `uri` with `request` (resolved against registers if `request.text`
  names one), then either store the response in register `dest.name`
  or write it to file `dest.path`.
- **`ExpressionDescriptor { name, expression }`** — S-expression /
  Lisp form. Not yet implemented.

`Data { type, text | binary }` is the universal value: a content-typed
blob that can be either text or binary. Registers store `Data`.

## Service

```proto
service Protosh {
  rpc Run(ScriptDescriptor) returns (Data);
}
```

`Run` executes the script's statements in order and returns the last
written `Data`.

## Go runtime (`protosh/`)

The in-process runtime is `protosh.Runtime`. It implements
`pb.ProtoshServer` so it can be mounted directly on a `grpc.Server`:

```go
runtime := protosh.New()
runtime.Register("grpc://astkit.Transformer/Find", findHandler)
runtime.Register("file://out.pb", writePBHandler)
pb.RegisterProtoshServer(grpcServer, runtime)
```

Dispatch is pluggable: callers register `Handler(ctx, *Data) (*Data, error)`
functions keyed by URI before calling `Run`. This lets the
Metaparser.Transform pipeline wire in-process handlers (such as
`astkit.Transformer` methods) directly.

## Status

- `ScriptDescriptor` schema complete.
- `Protosh.Run` implemented: Import, const/mutable Variable, Dispatch
  (register-resolved request, name-or-path destination).
- Expression interpreter not yet implemented.
- `go test ./protosh/` green (17 tests: unit + bufconn e2e).
