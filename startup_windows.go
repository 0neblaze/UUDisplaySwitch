package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

const startupTaskSource = "UUDisplaySwitch.Startup.v1"

func startupTaskNameForSID(sid string) string {
	return "UUDisplaySwitch-" + sid
}

func protectedMonitorDirForSID(sid string) string {
	return filepath.Join(os.Getenv("ProgramFiles"), "UUDisplaySwitch", sid)
}

func protectedMonitorPathForSID(sid string) string {
	return filepath.Join(protectedMonitorDirForSID(sid), "UUDisplaySwitch.exe")
}

func legacyProtectedMonitorPathForSID(sid string) string {
	return filepath.Join(protectedMonitorDirForSID(sid), "UUDisplayCore.exe")
}

func schtasksPath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "schtasks.exe")
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

func escapeXMLText(v string) string {
	return xmlEscaper.Replace(v)
}

func stripXMLComments(v string) string {
	for strings.Contains(v, "<!--") {
		start := strings.Index(v, "<!--")
		end := strings.Index(v[start+4:], "-->")
		if end < 0 {
			break
		}
		v = v[:start] + v[start+4+end+3:]
	}
	return v
}

func unescapeXMLText(v string) string {
	if strings.Contains(v, "<!--") {
		v = stripXMLComments(v)
	}
	if !strings.ContainsRune(v, '&') {
		return v
	}
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); {
		if v[i] == '&' {
			if semi := strings.IndexByte(v[i+1:], ';'); semi >= 0 && semi <= 12 {
				ent := v[i+1 : i+1+semi]
				switch ent {
				case "lt":
					b.WriteByte('<')
					i += semi + 2
					continue
				case "gt":
					b.WriteByte('>')
					i += semi + 2
					continue
				case "quot":
					b.WriteByte('"')
					i += semi + 2
					continue
				case "apos":
					b.WriteByte('\'')
					i += semi + 2
					continue
				case "amp":
					b.WriteByte('&')
					i += semi + 2
					continue
				}
				if strings.HasPrefix(ent, "#") && len(ent) > 1 {
					base := 10
					num := ent[1:]
					if strings.HasPrefix(num, "x") || strings.HasPrefix(num, "X") {
						base = 16
						num = num[1:]
					}
					if num != "" {
						if code, err := strconv.ParseInt(num, base, 32); err == nil && code > 0 && code <= utf8.MaxRune && utf8.ValidRune(rune(code)) {
							b.WriteRune(rune(code))
							i += semi + 2
							continue
						}
					}
				}
			}
		}
		b.WriteByte(v[i])
		i++
	}
	return b.String()
}

func createStartupTaskXML(sid, executable string) string {
	workDir := filepath.Dir(executable)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>UU virtual display: monitor main-process exit after Windows sign-in.</Description>
    <Source>%s</Source>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%s</UserId>
      <Delay>PT10S</Delay>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Owner">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Owner">
    <Exec>
      <Command>%s</Command>
      <Arguments>--watch</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>`, escapeXMLText(startupTaskSource), escapeXMLText(sid), escapeXMLText(sid), escapeXMLText(executable), escapeXMLText(workDir))
}

func skipXMLSpecial(s string, i int) (int, bool) {
	if strings.HasPrefix(s[i:], "<!--") {
		if end := strings.Index(s[i+4:], "-->"); end >= 0 {
			return i + 4 + end + 3, true
		}
		return len(s), true
	}
	if strings.HasPrefix(s[i:], "<?") {
		if end := strings.Index(s[i+2:], "?>"); end >= 0 {
			return i + 2 + end + 2, true
		}
		return len(s), true
	}
	if strings.HasPrefix(s[i:], "<![CDATA[") {
		if end := strings.Index(s[i+9:], "]]>"); end >= 0 {
			return i + 9 + end + 3, true
		}
		return len(s), true
	}
	return i, false
}

func scanXMLTagEnd(s string, start int) (int, bool) {
	var quote byte
	for i := start; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '>' {
			return i, true
		}
	}
	return -1, false
}

func parseXMLTagName(tagInner string) (closing bool, name string, selfClosing bool) {
	t := strings.TrimSpace(tagInner)
	if strings.HasPrefix(t, "/") {
		closing = true
		t = strings.TrimSpace(t[1:])
	} else if strings.HasSuffix(t, "/") {
		selfClosing = true
		t = strings.TrimSpace(t[:len(t)-1])
	}
	end := len(t)
	for i := 0; i < len(t); i++ {
		if t[i] == ' ' || t[i] == '\t' || t[i] == '\r' || t[i] == '\n' {
			end = i
			break
		}
	}
	name = t[:end]
	if colon := strings.LastIndexByte(name, ':'); colon >= 0 {
		name = name[colon+1:]
	}
	return closing, name, selfClosing
}

func scanDirectXMLElements(s string) ([]struct{ name, inner string }, error) {
	var elems []struct{ name, inner string }
	depth := 0
	var curName string
	var contentStart int
	for i := 0; i < len(s); {
		if s[i] != '<' {
			i++
			continue
		}
		if next, ok := skipXMLSpecial(s, i); ok {
			if next == len(s) {
				return nil, errors.New("unterminated XML comment or declaration")
			}
			i = next
			continue
		}
		gt, ok := scanXMLTagEnd(s, i+1)
		if !ok {
			return nil, errors.New("unterminated XML tag")
		}
		closing, name, selfClosing := parseXMLTagName(s[i+1 : gt])
		if name == "" {
			return nil, errors.New("empty XML tag name")
		}
		if closing {
			if depth == 0 {
				return nil, fmt.Errorf("unexpected closing tag </%s>", name)
			}
			depth--
			if depth == 0 {
				if name != curName {
					return nil, fmt.Errorf("mismatched closing tag </%s> for <%s>", name, curName)
				}
				elems = append(elems, struct{ name, inner string }{name: curName, inner: s[contentStart:i]})
			}
		} else if selfClosing {
			if depth == 0 {
				elems = append(elems, struct{ name, inner string }{name: name, inner: ""})
			}
		} else {
			if depth == 0 {
				curName = name
				contentStart = gt + 1
			}
			depth++
		}
		i = gt + 1
	}
	if depth != 0 {
		return nil, fmt.Errorf("unclosed XML tag <%s>", curName)
	}
	return elems, nil
}

func findChildXML(s, tag string) (string, bool, error) {
	elems, err := scanDirectXMLElements(s)
	if err != nil {
		return "", false, err
	}
	var found string
	count := 0
	for _, el := range elems {
		if el.name == tag {
			count++
			if count > 1 {
				return "", false, fmt.Errorf("duplicate XML element <%s>", tag)
			}
			found = el.inner
		}
	}
	return found, count == 1, nil
}

func normalizeTaskXMLBytes(raw []byte) []byte {
	// Handle UTF-16 LE BOM if ever returned directly by Task Scheduler APIs.
	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		u16 := make([]uint16, (len(raw)-2)/2)
		for i := range u16 {
			u16[i] = uint16(raw[2+i*2]) | uint16(raw[2+i*2+1])<<8
		}
		raw = []byte(string(utf16.Decode(u16)))
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	// schtasks /Query /XML outputs UTF-8/ACP text while leaving encoding="UTF-16" in the header.
	s := strings.Replace(string(raw), `encoding="UTF-16"`, `encoding="UTF-8"`, 1)
	s = strings.Replace(s, `encoding='UTF-16'`, `encoding='UTF-8'`, 1)
	return []byte(s)
}

func verifyOwnedStartupTaskXML(raw []byte, expectedSID string) (bool, error) {
	cleaned := string(normalizeTaskXMLBytes(raw))
	taskBody, hasTask, err := findChildXML(cleaned, "Task")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	if !hasTask {
		return false, errors.New("invalid scheduled task XML: missing Task root")
	}

	regInfo, _, err := findChildXML(taskBody, "RegistrationInfo")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	source, _, err := findChildXML(regInfo, "Source")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}

	principals, _, err := findChildXML(taskBody, "Principals")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	principal, _, err := findChildXML(principals, "Principal")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	userID, _, err := findChildXML(principal, "UserId")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	logonType, _, err := findChildXML(principal, "LogonType")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	runLevel, _, err := findChildXML(principal, "RunLevel")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}

	actionsBody, hasActions, err := findChildXML(taskBody, "Actions")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	var actionElems []struct{ name, inner string }
	if hasActions {
		actionElems, err = scanDirectXMLElements(actionsBody)
		if err != nil {
			return false, fmt.Errorf("invalid scheduled task XML: %w", err)
		}
	}

	expectedNew := protectedMonitorPathForSID(expectedSID)
	expectedLegacy := legacyProtectedMonitorPathForSID(expectedSID)
	if strings.TrimSpace(unescapeXMLText(source)) != startupTaskSource ||
		strings.TrimSpace(unescapeXMLText(userID)) != expectedSID ||
		strings.TrimSpace(unescapeXMLText(logonType)) != "InteractiveToken" ||
		strings.TrimSpace(unescapeXMLText(runLevel)) != "HighestAvailable" ||
		len(actionElems) != 1 ||
		actionElems[0].name != "Exec" {
		return false, errors.New("existing startup task has an unexpected owner or action; it was not modified")
	}

	cmdRaw, _, err := findChildXML(actionElems[0].inner, "Command")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	argsRaw, _, err := findChildXML(actionElems[0].inner, "Arguments")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	cmd := strings.TrimSpace(unescapeXMLText(cmdRaw))
	args := strings.TrimSpace(unescapeXMLText(argsRaw))
	if (!strings.EqualFold(cmd, expectedNew) && !strings.EqualFold(cmd, expectedLegacy)) || args != "--watch" {
		return false, errors.New("existing startup task has an unexpected executable or arguments; it was not modified")
	}

	triggersBody, hasTriggers, err := findChildXML(taskBody, "Triggers")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	if !hasTriggers {
		return false, nil
	}
	logonTrigger, hasLogonTrigger, err := findChildXML(triggersBody, "LogonTrigger")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	if !hasLogonTrigger {
		return false, nil
	}
	if trigEnabled, hasTrigEnabled, err := findChildXML(logonTrigger, "Enabled"); err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	} else if hasTrigEnabled && strings.EqualFold(strings.TrimSpace(unescapeXMLText(trigEnabled)), "false") {
		return false, nil
	}

	settingsBody, hasSettings, err := findChildXML(taskBody, "Settings")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	if !hasSettings {
		return true, nil
	}
	enabledVal, hasEnabled, err := findChildXML(settingsBody, "Enabled")
	if err != nil {
		return false, fmt.Errorf("invalid scheduled task XML: %w", err)
	}
	if !hasEnabled {
		return true, nil
	}
	return !strings.EqualFold(strings.TrimSpace(unescapeXMLText(enabledVal)), "false"), nil
}

func runHiddenCommand(exe string, args ...string) ([]byte, error) {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return cmd.CombinedOutput()
}

func isTaskNotFoundError(err error, out []byte) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || bytes.Contains(out, []byte("<Task")) {
		return false
	}
	text := strings.ToLower(string(out))
	osNotFound := strings.TrimRight(strings.ToLower(windows.ERROR_FILE_NOT_FOUND.Error()), ".。")
	if osNotFound != "" && strings.Contains(text, osNotFound) {
		return true
	}
	return strings.Contains(text, "cannot find the file specified") ||
		strings.Contains(text, "does not exist") ||
		strings.Contains(text, "找不到指定的文件") ||
		strings.Contains(text, "找不到指定的檔案")
}

func queryStartupTaskXML(taskName string) ([]byte, bool, error) {
	out, err := runHiddenCommand(schtasksPath(), "/Query", "/TN", taskName, "/XML")
	if err != nil {
		if isTaskNotFoundError(err, out) {
			return nil, false, nil
		}
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return nil, false, fmt.Errorf("failed to query startup task: %w (%s)", err, msg)
		}
		return nil, false, fmt.Errorf("failed to query startup task: %w", err)
	}
	return out, true, nil
}

func readStartupEnabled() (bool, error) {
	sid, err := currentUserSIDString()
	if err != nil {
		return false, err
	}
	xmlBytes, exists, err := queryStartupTaskXML(startupTaskNameForSID(sid))
	if err != nil || !exists {
		return false, err
	}
	return verifyOwnedStartupTaskXML(xmlBytes, sid)
}

func sha256File(path string) ([32]byte, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func copyFileVerified(src, dst string) error {
	srcSum, err := sha256File(src)
	if err != nil {
		return err
	}
	if dstSum, dstErr := sha256File(dst); dstErr == nil && dstSum == srcSum {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	dstSum, err := sha256File(dst)
	if err != nil {
		return err
	}
	if dstSum != srcSum {
		return errors.New("background component SHA-256 verification failed after copy")
	}
	return nil
}

func protectStartupPathACL(path string, isDirectory bool, userSID string) error {
	var sddl string
	if isDirectory {
		sddl = fmt.Sprintf("O:BAD:P(A;OICI;FA;;;BA)(A;OICI;FA;;;SY)(A;OICI;0x1200a9;;;%s)", userSID)
	} else {
		sddl = fmt.Sprintf("O:BAD:P(A;;FA;;;BA)(A;;FA;;;SY)(A;;0x1200a9;;;%s)", userSID)
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("build startup ACL: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("read startup ACL owner: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read startup ACL DACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner,
		nil,
		dacl,
		nil,
	)
}

func configureStartup(enable bool, expectedSID string) error {
	if globalReadOnly {
		return errors.New("read-only mode rejects startup changes")
	}
	sid, err := currentUserSIDString()
	if err != nil {
		return err
	}
	if expectedSID != "" && sid != expectedSID {
		return errors.New("authorize with the same Windows account that opened this application")
	}
	if !elevated() {
		return errors.New("administrator permission is required to configure sign-in startup")
	}

	taskName := startupTaskNameForSID(sid)
	existingXML, exists, err := queryStartupTaskXML(taskName)
	if err != nil {
		return err
	}
	if exists {
		if _, err := verifyOwnedStartupTaskXML(existingXML, sid); err != nil {
			return err
		}
	}
	if !enable {
		if !exists {
			return nil
		}
		if out, delErr := runHiddenCommand(schtasksPath(), "/Delete", "/TN", taskName, "/F"); delErr != nil {
			return fmt.Errorf("could not remove startup task: %w (%s)", delErr, strings.TrimSpace(string(out)))
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := protectedMonitorDirForSID(sid)
	targetExe := protectedMonitorPathForSID(sid)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := protectStartupPathACL(dir, true, sid); err != nil {
		return err
	}
	if srcSum, sErr := sha256File(exe); sErr == nil {
		if dstSum, dErr := sha256File(targetExe); dErr != nil || dstSum != srcSum {
			if active := readHeartbeat(); active != nil {
				if img, imgErr := processImagePath(active.PID); imgErr == nil && sameTrayPath(img, targetExe) {
					if stopErr := stopUUWatch(); stopErr != nil {
						return fmt.Errorf("failed to stop running startup monitor before updating binary: %w", stopErr)
					}
				}
			}
		}
	}
	if err := copyFileVerified(exe, targetExe); err != nil {
		if mExists, _ := monitorMutexExists(); mExists {
			if stopErr := stopUUWatch(); stopErr == nil {
				err = copyFileVerified(exe, targetExe)
			}
		}
		if err != nil {
			return err
		}
	}
	if err := protectStartupPathACL(targetExe, false, sid); err != nil {
		return err
	}

	xmlPayload := createStartupTaskXML(sid, targetExe)
	xmlTmp := filepath.Join(dir, "task-register.xml")
	if err := os.WriteFile(xmlTmp, []byte(xmlPayload), 0600); err != nil {
		return err
	}
	defer os.Remove(xmlTmp)

	if out, createErr := runHiddenCommand(schtasksPath(), "/Create", "/TN", taskName, "/XML", xmlTmp, "/F"); createErr != nil {
		return fmt.Errorf("could not register startup task: %w (%s)", createErr, strings.TrimSpace(string(out)))
	}
	confirmed, err := readStartupEnabled()
	if err != nil {
		return err
	}
	if !confirmed {
		return errors.New("startup registration could not be confirmed")
	}
	return nil
}

type startupHelperResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func runStartupHelperCommand(mode, ownerSID, requestID string) int {
	if globalReadOnly {
		return 13
	}
	if !validMonitorToken(requestID) {
		return 14
	}
	if mode != "enable" && mode != "disable" {
		return 14
	}
	err := configureStartup(mode == "enable", ownerSID)
	res := startupHelperResult{OK: err == nil}
	if err != nil {
		res.Message = err.Error()
	}
	_ = os.MkdirAll(dataDirectory(), 0700)
	_ = writeJSONAtomically(filepath.Join(dataDirectory(), "startup-"+requestID+".json"), res)
	if err != nil {
		return 10
	}
	return 0
}

func changeStartup(enable bool, ownerHwnd uintptr) error {
	if globalReadOnly {
		return errors.New("read-only mode rejects startup changes")
	}
	sid, err := currentUserSIDString()
	if err != nil {
		return err
	}
	if elevated() {
		return configureStartup(enable, sid)
	}

	reqToken, err := randomMonitorToken()
	if err != nil {
		return err
	}
	resultPath := filepath.Join(dataDirectory(), "startup-"+reqToken+".json")
	defer os.Remove(resultPath)

	mode := "disable"
	if enable {
		mode = "enable"
	}
	proc, err := launchElevated(fmt.Sprintf("--startup %s --owner %s --request %s", mode, sid, reqToken), ownerHwnd)
	if err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("已取消管理员授权。")
		}
		return fmt.Errorf("无法请求管理员权限：%w", err)
	}
	if proc == 0 {
		return errors.New("无法取得授权子进程句柄。")
	}
	defer windows.CloseHandle(proc)
	wait, err := windows.WaitForSingleObject(proc, 60000)
	if err != nil {
		return err
	}
	if wait == uint32(windows.WAIT_TIMEOUT) {
		return errors.New("管理员子进程超时未完成。")
	}
	var exitCode uint32
	_ = windows.GetExitCodeProcess(proc, &exitCode)

	raw, err := os.ReadFile(resultPath)
	if err != nil {
		return fmt.Errorf("startup helper exited with code %d without a result: %w", exitCode, err)
	}
	var parsed startupHelperResult
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	if !parsed.OK {
		return errors.New(parsed.Message)
	}
	confirmed, err := readStartupEnabled()
	if err != nil {
		return err
	}
	if confirmed != enable {
		return errors.New("startup state did not match requested state")
	}
	return nil
}
