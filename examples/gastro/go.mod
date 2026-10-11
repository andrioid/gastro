module gastro-website

go 1.27.2

require (
	github.com/alecthomas/chroma/v2 v2.23.1
	github.com/andrioid/gastro v0.0.0
	github.com/andrioid/gastro/datastar v0.0.0
	github.com/andrioid/gastro/pkg/chromalexer/gastro v0.0.0
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510
	github.com/yuin/goldmark v1.8.2
	github.com/yuin/goldmark-highlighting/v2 v2.0.0-20230729083705-37449abec8cc
)

require (
	github.com/CAFxX/httpcompression v0.0.9 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/starfederation/datastar-go v1.2.2 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
)

replace github.com/andrioid/gastro => ../..

replace github.com/andrioid/gastro/datastar => ../../datastar

replace github.com/andrioid/gastro/pkg/chromalexer/gastro => ../../pkg/chromalexer/gastro

tool github.com/andrioid/gastro/cmd/gastro
