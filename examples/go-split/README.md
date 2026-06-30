# Go Split Target Example

This example starts two Go HTTP servers:

```txt
https://go-split.localhost/      -> frontend target on 127.0.0.1:5173
https://go-split.localhost/api/* -> API target on 127.0.0.1:8080
```

## Run

Terminal 1:

```bash
cd examples/go-split
go run .
```

Terminal 2:

```bash
cd examples/go-split
../../routeup serve
```

Open:

```txt
https://go-split.localhost
```

Or test directly:

```bash
curl https://go-split.localhost/api/message
```
