package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
)

func main() {
    fmt.Println("🔍 Solana Latency Research started…")

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    conn, err := grpc.DialContext(
        ctx,
        "solana-yellowstone-grpc.publicnode.com:443",
        grpc.WithTransportCredentials(insecure.NewCredentials()),
    )
    if err != nil {
        log.Fatalf("failed to connect: %v", err)
    }
    defer conn.Close()

    fmt.Println("✅ Connected to Solana gRPC node")

    // TODO: 订阅槽位或交易流并衡量延迟
    for {
        time.Sleep(5 * time.Second)
        fmt.Println("Measuring latency…")
    }
}
