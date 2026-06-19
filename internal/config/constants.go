package config

type Source string

const (
	SourceRouteupJSON Source = "routeup.json"
	SourcePackageJSON Source = "package.json"
	SourceNone        Source = ""
)
