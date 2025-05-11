package logging

import (
    "io"
    "log"
    "os"

    "gopkg.in/natefinch/lumberjack.v2"
)

var (
    Info  *log.Logger
    Warn  *log.Logger
    Error *log.Logger
)

func SetupLogger(logFile string) {
    lumberjackLogger := &lumberjack.Logger{
        Filename:   logFile,
        MaxSize:    100, // megabytes
        MaxBackups: 7,
        MaxAge:     28,   //days
        Compress:   true, // disabled by default
    }

    multiWriter := io.MultiWriter(os.Stdout, lumberjackLogger)

    Info = log.New(multiWriter, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
    Warn = log.New(multiWriter, "WARN: ", log.Ldate|log.Ltime|log.Lshortfile)
    Error = log.New(multiWriter, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
}
