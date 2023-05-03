#!/bin/bash
mkdir -p ./proto
protoc --go_out=./proto \
       --proto_path /home/geoff/splitflap/proto:/home/geoff/splitflap/thirdparty/nanopb/generator/proto \
       --go_opt Msplitflap.proto=github.com/trapgate/flapperd/proto \
       --go_opt Mnanopb.proto=github.com/trapgate/flapperd/proto \
       --go_opt paths=source_relative \
       /home/geoff/splitflap/proto/splitflap.proto \
       /home/geoff/splitflap/thirdparty/nanopb/generator/proto/nanopb.proto
