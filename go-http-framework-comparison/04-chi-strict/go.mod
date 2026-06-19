module github.com/kitchen-sink/04-chi-strict

go 1.24.0

require (
	github.com/go-chi/chi/v5 v5.1.0
	github.com/kitchen-sink/shared v0.0.0
	github.com/oapi-codegen/runtime v1.4.1
)

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
)

replace github.com/kitchen-sink/shared => ../shared
