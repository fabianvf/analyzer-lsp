# Analyzer Rule Engine 

In this project, we are writing a rule engine that can use pluggable providers for rules to make a consistent way to execute rules for Konveyor. 

One of the primary drivers for this repository is adding providers for specific languages using the Language Server Protocol. Today these providers are in tree, but we will be moving them out in the future.

## Quick Demo

If you would like to run a quick demo we have a Dockerfile that has all the dependencies.

To run this demo build the container:

```
$ podman build -f Dockerfile -t test-analyzer-engine
```

This will build the engine, and include the current set of rules and examples in the container to be used. 

To run the rules (rule-example.yaml) against the examples:

```
$ podman run test-analyzer-engine
```

## Running from source code

To run the engine from source code, you need to:

* Configure providers. By default, providers are configured in `provider_settings.json`. See [Providers](#./docs/providers.md) for instructions on configuring providers.
* Configure rules. By default, rules are present in `rules_example.yaml`. See [Rules](#./docs/rules.md) for details on rule format. 

Once the providers are configured, you can run:

```sh
go run cmd/analyzer/main.go
```

CLI Options:

```sh
  --provider-settings string   path to the provider settings (default "provider_settings.json")
  --rules stringArray          filename or directory containing rule files (default [rule-example.yaml])
  --output-file string         filepath to to store rule violations (default "output.yaml")
  --label-selector string      an expression to select rules based on labels
  --enable-jaeger              enable tracer exports to jaeger endpoint
  --error-on-violation         exit with 3 if any violation are found will also print violations to console
  --jaeger-endpoint string     jaeger endpoint to collect tracing data (default "http://localhost:14268/api/traces")
  --verbose int                level for logging output (default 9)
```

* See [label selector](./docs/labels.md#label-selector) for more info on `--label-selector` option.

## Code Base Starting Point

 Using the LSP/Protocal from ACME https://github.com/fhs/acme-lsp and stripping out anything related to serving, proxy or anything. Just keeping the types for communication

 Using JSONRPC2 from google.org/x/tools/internal. Copied and removed anything to do with serving. 


## Code of Conduct

Refer to Konveyor's [Code of Conduct page](https://github.com/konveyor/community/blob/main/CODE_OF_CONDUCT.md)
