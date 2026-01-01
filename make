#!/usr/bin/env sh
which go || echo "Please install go"  exit 1
go build -o fchroot src/main/main.go
