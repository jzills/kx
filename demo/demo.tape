Output demo/demo.gif

Set FontSize 14
Set Width 1024
Set Height 700
Set FontFamily "Ubuntu Mono"
Set Framerate 30
Set Theme {"background":"#000000","foreground":"#e6edf3","cursor":"#3fb950","black":"#000000","red":"#f85149","green":"#3fb950","yellow":"#e3b341","blue":"#58a6ff","magenta":"#bc8cff","cyan":"#39c5cf","white":"#e6edf3","brightBlack":"#6e7681","brightRed":"#f85149","brightGreen":"#3fb950","brightYellow":"#e3b341","brightBlue":"#58a6ff","brightMagenta":"#bc8cff","brightCyan":"#39c5cf","brightWhite":"#ffffff"}

Type "kx get pods"
Enter
Sleep 2.5s

Type "kx labels 2"
Enter
Sleep 2s

Type "kx logs 2 --tail=2"
Enter
Sleep 2.5s

Type "kx top"
Enter
Sleep 2.5s

Type "kx get deployments"
Enter
Sleep 2s

Type "kx tree 7"
Enter
Sleep 3s

Type "kx diag"
Enter
Sleep 4s
