default:
    @just --list

c-test:
    mkdir -p tests/c_unit/build
    cc -Wall -Wextra -Werror -std=c99 -pedantic -fsanitize=address,undefined \
        -Iruntime runtime/ob_varint.c runtime/ob_encode.c runtime/ob_decode.c \
        tests/c_unit/test_runtime.c -o tests/c_unit/build/test_runtime
    ./tests/c_unit/build/test_runtime

test: c-test
    go test -v ./...

lint:
    cd proto && buf lint && buf build

bench:
    bash benchmarks/run_bench.sh
