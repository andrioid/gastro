module dashboard

go 1.26.1

require github.com/andrioid/gastro v0.0.0

require (
	github.com/alecthomas/chroma/v2 v2.23.1 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/yuin/goldmark v1.8.2 // indirect
	github.com/yuin/goldmark-highlighting/v2 v2.0.0-20230729083705-37449abec8cc // indirect
)

replace github.com/andrioid/gastro => ../..

tool github.com/andrioid/gastro/cmd/gastro
