module capital_observatory/plugins/macro

go 1.22

require (
	capital_observatory v0.0.0
	github.com/rs/zerolog v1.33.0
	google.golang.org/grpc v1.65.0
)

replace capital_observatory => ../..
