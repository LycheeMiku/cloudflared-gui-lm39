Set WshShell = CreateObject("WScript.Shell")
ScriptDir = CreateObject("Scripting.FileSystemObject").GetParentFolderName(WScript.ScriptFullName)
WshShell.CurrentDirectory = ScriptDir

' Run cloudflared gui completely hidden (window style 0 = hidden, no terminal window)
WshShell.Run "cloudflared.exe gui", 0, False
