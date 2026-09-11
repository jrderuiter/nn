package nono

import "os"

func osEnviron() []string { return os.Environ() }
