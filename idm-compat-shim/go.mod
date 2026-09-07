module idm-compat-shim

go 1.21

require (
	github.com/gorilla/websocket v1.5.3
	idm-shared v0.0.0-00010101000000-000000000000
)

replace idm-shared => ../idm-shared
