package probe

// The generated Go file embeds the verifier-ready BPF object so production
// builds and integration probes do not require clang on the Lazycat box.
//
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -target bpfel -cflags "-O2 -g -Wall -Werror" hostlimit ./bpf/hostlimit.c -- -I/usr/include/x86_64-linux-gnu
