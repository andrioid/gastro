module dashboard

go 1.27.2

require (
	github.com/andrioid/gastro v0.0.0
	github.com/andrioid/gastro/datastar v0.0.0
)

require (
	github.com/CAFxX/httpcompression v0.0.9 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/starfederation/datastar-go v1.2.2 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
)

replace github.com/andrioid/gastro => ../..

replace github.com/andrioid/gastro/datastar => ../../datastar

tool github.com/andrioid/gastro/cmd/gastro
