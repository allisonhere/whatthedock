package systems

import (
	"os/user"
	"testing"

	"github.com/allisonhere/whatthedock/internal/config"
)

func TestSSHLoginUserDefaultsToTheOSUser(t *testing.T) {
	if got := sshLoginUser(config.System{SSHUser: "allie"}); got != "allie" {
		t.Fatalf("explicit user = %q, want allie", got)
	}
	current, err := user.Current()
	if err != nil {
		t.Skip("no current user:", err)
	}
	if got := sshLoginUser(config.System{}); got != current.Username {
		t.Fatalf("empty SSHUser gave %q, want the OS user %q (an empty name makes the server reject the login)", got, current.Username)
	}
}
