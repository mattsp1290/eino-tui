package cli

import (
	"errors"

	"github.com/mattsp1290/eino-tui/internal/codexmodel"
)

type Command uint8

const (
	CommandChat Command = iota
	CommandLogin
	CommandStatus
	CommandHelp
	CommandVersion
)

type Options struct {
	Command Command
	Model   string
}

var ErrInvalidArguments = errors.New("invalid arguments")

func Parse(args []string) (Options, error) {
	result := Options{Command: CommandChat, Model: codexmodel.DefaultModel}
	if len(args) == 0 {
		return result, nil
	}
	if len(args) == 1 {
		switch args[0] {
		case "login":
			return Options{Command: CommandLogin}, nil
		case "status":
			return Options{Command: CommandStatus}, nil
		case "-h", "--help":
			return Options{Command: CommandHelp}, nil
		case "-v", "--version":
			return Options{Command: CommandVersion}, nil
		}
	}
	if len(args) == 2 && args[0] == "--model" && codexmodel.ValidateModel(args[1]) == nil {
		result.Model = args[1]
		return result, nil
	}
	return Options{}, ErrInvalidArguments
}
