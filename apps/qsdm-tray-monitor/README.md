# QSDM Tray Monitor

Tiny Windows notification-area monitor for the local QSDM home node.

It watches:

- local validator readiness on `127.0.0.1:8080`
- QSDMMiner service/process state
- home gateway process and public relay status
- local GUI process

The app has no normal Exit command. It is tray-only and meant to stay running.
It can still be stopped by the operator with Task Manager or by removing its
Startup launcher.

Build:

```powershell
dotnet publish .\qsdm-tray-monitor.csproj -c Release -r win-x64 --self-contained false -p:PublishSingleFile=true -o .\dist
```

Install at user logon:

```powershell
.\install_startup.ps1
```
