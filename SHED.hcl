application "shed" {
  content {
    include = [
      ".github",
      ".gitmodules",
      ".goreleaser.yaml",
      "AGENTS.md",
      "README.md",
      "Taskfile.yml",
      "cmd",
      "docs",
      "go.mod",
      "go.sum",
      "internal",
      "testdata",
      "third_party",
    ]
  }

  build {
    image = "golang:1.26"
    commands = [
      ["go", "mod", "download"],
      ["go", "build", "-ldflags=-w -s", "-o", "out", "./cmd/shed"],
    ]
  }

  run {
    command           = ["./out"]
    port              = 8080
    working_directory = "/app"
    user              = "1000:1000"
    environment = {
      PORT             = "8080"
      RAILPACK_VERSION = "shed"
    }
    stop_signal = "SIGTERM"
  }
}
