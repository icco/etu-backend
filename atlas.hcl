env "local" {
  src = getenv("ATLAS_MODELS_URL")
  dev = getenv("ATLAS_DEV_URL")
  migration {
    dir = "file://migrations"
  }
}
