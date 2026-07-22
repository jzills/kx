Output demo/diag.gif

Set FontSize 16
Set Width 1200
Set Height 750
Set FontFamily "Ubuntu Mono"
Set Framerate 30
Set Theme {"background":"#000000","foreground":"#e6edf3","cursor":"#3fb950","black":"#000000","red":"#f85149","green":"#3fb950","yellow":"#e3b341","blue":"#58a6ff","magenta":"#bc8cff","cyan":"#39c5cf","white":"#e6edf3","brightBlack":"#6e7681","brightRed":"#f85149","brightGreen":"#3fb950","brightYellow":"#e3b341","brightBlue":"#58a6ff","brightMagenta":"#bc8cff","brightCyan":"#39c5cf","brightWhite":"#ffffff"}

Hide
Type "VIRTUAL_ENV_DISABLE_PROMPT=1 source .venv/bin/activate && clear"
Enter
Sleep 500ms
Show

Type "kx diag"
Enter
Sleep 4s

Type "kx diag 6"
Enter
Sleep 4.5s

Type "kx logs 6 --tail=3"
Enter
Sleep 3s

Type "kx diag 8"
Enter
Sleep 3.5s
