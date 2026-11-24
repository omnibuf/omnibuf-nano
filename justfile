default:
    @just --list

c-test:
    mkdir -p tests/c_unit/build
    cc -Wall -Wextra -Werror -std=c99 -pedantic \
        -Wshadow -Wundef -Wcast-align -Wstrict-aliasing=2 -Wdouble-promotion \
        -fsanitize=address,undefined \
        -Iruntime runtime/ob_varint.c runtime/ob_encode.c runtime/ob_decode.c \
        tests/c_unit/test_runtime.c -o tests/c_unit/build/test_runtime
    ./tests/c_unit/build/test_runtime

test: c-test
    go test -v ./...

lint:
    cd proto && buf lint && buf build
    @unformatted=$(gofmt -l .); if [ -n "$unformatted" ]; then echo "gofmt needed:"; echo "$unformatted"; exit 1; fi
    go vet ./...
    cc -fsyntax-only -Wall -Wextra -Werror -std=c99 -pedantic \
        -Wshadow -Wundef -Wcast-align -Wstrict-aliasing=2 -Wdouble-promotion \
        -Iruntime runtime/ob_varint.c runtime/ob_encode.c runtime/ob_decode.c \
        tests/c_unit/test_runtime.c
    cc -fsyntax-only -Wall -Wextra -Werror -std=c99 -pedantic -fshort-enums \
        -Iruntime runtime/ob_varint.c runtime/ob_encode.c runtime/ob_decode.c \
        tests/c_unit/test_runtime.c

ci: lint test

bench:
    bash benchmarks/run_bench.sh
