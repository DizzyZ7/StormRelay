# StormRelay Go SDK

The Go client is part of the root module:

```go
client, err := stormrelay.NewClient("https://stormrelay.example", os.Getenv("STORMRELAY_API_KEY"))
if err != nil {
    log.Fatal(err)
}
incidents, err := client.ListIncidents(ctx, "detected", "critical", "checkout", "", 50)
```

The client uses caller-provided contexts, bounded response reads, bearer authentication, and typed `*stormrelay.APIError`. It performs no hidden retries; callers must decide whether an operation is safe to repeat.
