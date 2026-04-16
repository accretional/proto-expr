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
rt := protosh.New()
rt.Register("astkit://Filter", filterHandler)
rt.Register("astkit://ReplaceKind", replaceKindHandler)
pb.RegisterProtoshServer(grpcServer, rt)
```

Dispatch is pluggable: callers register `Handler(ctx, *Data) (*Data, error)`
functions keyed by URI before calling `Run`. URIs are matched exactly —
no scheme parsing or prefix matching — so hosts are free to design
their own scheme conventions (e.g. `astkit://<Method>`, `protoc://Compile`).

### Register resolution

`DispatchDescriptor.request` is a `Data`. When the `Data`'s encoding
is `text` and the text names a register, the runtime substitutes the
register's encoding into the request — letting scripts chain
dispatches like this:

```textproto
# pipeline: filter whitespace nodes, then rename keyword → kw
statements: {
  dispatch: {
    uri: "astkit://Filter"
    request: { type: "kind=whitespace", text: "ast" }
    name: "ast"
  }
}
statements: {
  dispatch: {
    uri: "astkit://ReplaceKind"
    request: { type: "from=keyword,to=kw", text: "ast" }
    name: "ast"
  }
}
```

The caller's `Data.type` is preserved when non-empty, even when the
encoding is substituted. This is load-bearing: `Data.type` doubles as
a lightweight parameter-pack channel (`k=v,k2=v2`) that handlers can
parse without the script needing a separate plumbing layer.

### Destinations

`DispatchDescriptor.dest` is either `name` (write response into a
register) or `path` (write response bytes to a file via `writePath`,
creating parent dirs as needed). Omit both to use the response only
as the final return value.

### URIs

- `Import`: `readURI` supports `file://` and bare paths today. Unknown
  schemes return an error.
- `Dispatch`: arbitrary — whatever the host registered.

## Host embedding example

Gluon v2's `Metaparser.Transform` RPC embeds Protosh as a library.
It parses a textproto `ScriptDescriptor` out of the request, wires
`v2/astkit`'s Transformer methods under `astkit://<Method>`, pre-loads
the request's `ASTNode` into the `ast` register, and returns the
final `Data`. See `gluon/v2/metaparser/transform.go` for the wiring
pattern.

## Status

- `ScriptDescriptor` schema complete.
- `Protosh.Run` implemented: Import, const/mutable Variable, Dispatch
  (register-resolved request, name-or-path destination).
- Expression interpreter not yet implemented — a script containing
  an `ExpressionDescriptor` statement returns
  `"expression: not yet implemented"`.
- `go test ./protosh/` green (17 tests: unit + bufconn e2e).
