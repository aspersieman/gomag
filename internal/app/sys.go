package app

import "syscall"

func syscallClose(fd int) { syscall.Close(fd) }
