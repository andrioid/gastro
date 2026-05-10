module gastro-website

go 1.26.1

require (
	github.com/alecthomas/chroma/v2 v2.23.1
	github.com/andrioid/gastro v0.0.0
	github.com/yuin/goldmark v1.8.2
	github.com/yuin/goldmark-highlighting/v2 v2.0.0-20230729083705-37449abec8cc
)

require (
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
)

replace github.com/andrioid/gastro => ../..

tool github.com/andrioid/gastro/cmd/gastro
