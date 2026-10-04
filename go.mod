module github.com/caffeine-projects/vlatte

go 1.27.0

ignore (
	./.idea
	./.local
	./.vscode
)

tool mvdan.cc/gofumpt

require (
	github.com/go-chi/chi/v5 v5.3.2
	github.com/google/go-jsonnet v0.22.0
	github.com/mattn/go-colorable v0.1.15
	github.com/mattn/go-isatty v0.0.24
	github.com/ohler55/ojg v1.28.7
	github.com/rs/zerolog v1.35.1
	github.com/ryanuber/go-glob v1.0.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/spf13/viper v1.21.0
	github.com/stretchr/testify v1.12.1
	go.uber.org/goleak v1.3.0
	google.golang.org/grpc v1.85.0-dev.0.20260825072537-93e31b48545e
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/sagikazarmark/locafero v0.12.0 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc // indirect
	mvdan.cc/gofumpt v0.12.0 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
