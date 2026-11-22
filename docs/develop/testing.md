# Testing

CI fails if unit-test coverage drops below 100%. Every code change needs tests.

## Run the tests

Unit tests take about 3 seconds:

    GOWORK=off go test ./...

End-to-end tests build and run the real program:

    GOWORK=off go test -tags e2e -count=1 -v ./...

Check coverage:

    GOWORK=off go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | tail -1

The last line must say `100.0%`. To see which functions are missing tests:

    go tool cover -func=coverage.out | awk '$3 != "100.0%"'

## How the tests avoid touching your computer

The program controls services, installs software and replaces itself. Tests must not do any of that for
real. So every operating-system call goes through a variable in `seams.go`, and tests replace those
variables.

- To pretend to be another operating system, use `withOS(t, "windows", "amd64")`.
- To fake a command such as `launchctl` or `brew`, use `installFakeExec(t, handler)`.
- To pretend the program is installed somewhere, use `withExecutable(t, path, nil)`.
- To start with empty home and cache folders, use `isolateHome(t)`.
- To fake a network service, use `httptest.NewServer(…)`.

These helpers are in `harness_test.go`.

## Write a test

This example tests a command that runs `systemctl`:

    func TestServiceStartLinux(t *testing.T) {
    	serviceEnv(t, "linux") // fake Linux, empty home, fast timeouts
    	fx := installFakeExec(t, func(cmd string) fakeResult { return ok("") })

    	if err := startService(); err != nil {
    		t.Fatal(err)
    	}
    	if !fx.called("systemctl --user start gcse-revision.service") {
    		t.Fatalf("calls = %v", fx.Calls())
    	}

    	// The failure path needs a test too.
    	installFakeExec(t, func(string) fakeResult { return fail("denied") })
    	if err := startService(); err == nil {
    		t.Fatal("expected an error")
    	}
    }

The handler receives the command as one string, for example `"systemctl --user start gcse-revision.service"`.
It returns `ok(output)`, `fail(output)` or `fakeResult{stdout, stderr, code}`.

## When code is hard to test

- If it calls the operating system, add a variable to `seams.go` and use it.
- If a branch can never run, delete it.
- Don't lower the coverage threshold or exclude files. Weakening CI checks needs explicit approval (see
  `AGENTS.md`).

## Before you push

    gofmt -l .
    GOWORK=off go vet ./... && GOWORK=off go vet -tags e2e ./...
    GOOS=windows GOWORK=off go vet ./...
    GOWORK=off go test -count=1 ./...

Don't use Go features newer than the version in `go.mod`. For example, use the `chdir` test helper instead of
`t.Chdir`.
