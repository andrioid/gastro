module gastro-website

go 1.26.1

require github.com/andrioid/gastro v0.0.0

require github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect

replace github.com/andrioid/gastro => ../..

tool github.com/andrioid/gastro/cmd/gastro
