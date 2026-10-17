package main

// dolphin2's golden transcripts, moved from ww-multiplayer's
// cli_golden_test.go with the command (Phase D6).

func TestCLIGoldenDolphin2(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "Dolphin-x64") // a directory: never executable
	iso := filepath.Join(root, "patched.iso")
	ud1 := filepath.Join(root, "ud1")
	ud2 := filepath.Join(root, "ud2")
	sav := filepath.Join(root, "start.sav")
	base := func(extra map[string]string) map[string]string {
		env := configEnv(filepath.Join(root, "config"))
		for k, v := range map[string]string{"DOLPHIN_EXE": exe, "ISO_PATH": iso, "USER_DIR_1": ud1, "USER_DIR_2": ud2} {
			env[k] = v
		}
		for k, v := range extra {
			env[k] = v
		}
		return env
	}
	run := func(name string, env map[string]string, args ...string) {
		t.Helper()
		o, code := runCLI(t, env, append([]string{"dolphin2"}, args...)...)
		checkCLIGolden(t, name, normalizeCLI(o, root), code)
	}

	run("dolphin2-no-exe", base(nil))
	if err := os.MkdirAll(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	run("dolphin2-no-iso", base(nil))
	writeCLIFile(t, iso, []byte("not a disc"))
	run("dolphin2-no-save-state", base(map[string]string{"SAVE_STATE": sav}))
	run("dolphin2-no-save-state-2", base(map[string]string{"SAVE_STATE_2": sav}))
	writeCLIFile(t, sav, []byte("state"))
	run("dolphin2-save-state-2-alone", base(map[string]string{"SAVE_STATE_2": sav}))
	run("dolphin2-no-user-dir", base(nil))

	// --reset removes USER_DIR_2 before the checks.
	writeCLIFile(t, filepath.Join(ud2, "Config", "Dolphin.ini"), []byte("[Core]\n"))
	env := base(map[string]string{"DOLPHIN_EXE": filepath.Join(root, "missing.exe")})
	run("dolphin2-reset", env, "--reset")
	if _, err := os.Stat(ud2); !os.IsNotExist(err) {
		t.Errorf("--reset left %s: %v", ud2, err)
	}

	// The bootstrap copies USER_DIR_1 (but its Cache) to USER_DIR_2 and
	// installs the cheats in both; then the launch of the directory fails.
	writeCLIFile(t, filepath.Join(ud1, "Config", "Dolphin.ini"), []byte("[Core]\nSlotA = 8\n"))
	writeCLIFile(t, filepath.Join(ud1, "Cache", "GZLE01.uidcache"), []byte("cache"))
	if runtime.GOOS != "windows" {
		t.Skip("the launching case is recorded on Windows only (exec errors differ by OS)")
	}
	if pids, err := emu.ListPIDs(); err == nil && len(pids) > 0 {
		t.Skipf("a Dolphin is running (pids %v): dolphin2 would plan around it", pids)
	}
	run("dolphin2-bootstrap-windows", base(nil))
	if _, err := os.Stat(filepath.Join(ud2, "Cache")); !os.IsNotExist(err) {
		t.Errorf("bootstrap copied Cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ud2, "Config", "Dolphin.ini")); err != nil {
		t.Errorf("after bootstrap: %v", err)
	}
	for _, ud := range []string{ud1, ud2} {
		if b, err := os.ReadFile(filepath.Join(ud, "GameSettings", "GZLE01.ini")); err != nil || !bytes.Equal(b, bundledCheatsIni) {
			t.Errorf("cheats in %s: %d bytes, %v", ud, len(b), err)
		}
	}
}
