# Contributing guidelines

Thanks for your interest! This is an independent, community-maintained project
(Apache-2.0). Contributions are welcome.

## Developer Certificate of Origin (DCO)

This project uses the [Developer Certificate of Origin](https://developercertificate.org/)
instead of a CLA. Sign off every commit to certify you wrote the patch (or have
the right to submit it under the project's license):

```sh
git commit -s -m "your message"   # adds a Signed-off-by trailer
```

## Contributing a patch

1. Open an issue describing the change you'd like to make.
2. Fork the repo, create a branch, develop and test:
   - `make test` — unit + envtest (controller logic).
   - `make test-kind-smoke` — runs the operator against a throwaway kind cluster
     and asserts it reconciles all objects (hermetic; no Alibaba Cloud creds).
   - `make lint` / `make fmt` / `make vet` — CI enforces these.
   - If you change API types or RBAC markers, run `make manifests generate` and
     commit the result (CI fails on drift).
3. Submit a pull request with DCO sign-off. The maintainer ([OWNERS](OWNERS))
   will review.

See [README](README.md) for architecture and deployment.
