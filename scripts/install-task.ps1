<#
.SYNOPSIS
  Installs Mayank 2.0 as a start-at-logon task and sets the Windows power settings.

.DESCRIPTION
  Registers (or replaces) the Task Scheduler task "Mayank2" for the current user:
    - trigger: at logon of this user
    - action:  bin\mayank2.exe run, working directory = repo root
    - restart on failure 3 times, every 5 minutes; no execution time limit;
      a second start while running is ignored (MultipleInstances IgnoreNew)
  Sets the current power plan (AC only):
    - sleep after: never, hibernate after: never, lid close: do nothing
  Prints a reminder to set the 80% battery charge limit in the vendor app.

  Safe to run again: the task is replaced in place and power values are absolute.
  -WhatIf prints every action and changes nothing.
  -Uninstall removes the task. Power settings are left as they are (the script
  does not know your previous values); change them in Settings > Power if needed.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File scripts\install-task.ps1 -WhatIf
  powershell -ExecutionPolicy Bypass -File scripts\install-task.ps1
  powershell -ExecutionPolicy Bypass -File scripts\install-task.ps1 -Uninstall
#>
[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [switch]$Uninstall,
    # Repo root; defaults to the parent of this scripts\ folder.
    [string]$RepoRoot = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$TaskName = 'Mayank2'
# $PSScriptRoot is empty in param defaults on Windows PowerShell 5.1 (-File), so resolve here.
if (-not $RepoRoot) { $RepoRoot = Split-Path -Parent $PSScriptRoot }
$RepoRoot = (Resolve-Path -LiteralPath $RepoRoot).Path
$Exe = Join-Path $RepoRoot 'bin\mayank2.exe'
$User = if ($env:USERDOMAIN) { "$env:USERDOMAIN\$env:USERNAME" } else { $env:USERNAME }

# powercfg with a fixed argument list; fails on a non-zero exit code.
function Invoke-PowerCfg([string[]]$Arguments) {
    $desc = "powercfg $($Arguments -join ' ')"
    if ($PSCmdlet.ShouldProcess('current power plan', $desc)) {
        & powercfg @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "install-task: '$desc' failed with exit code $LASTEXITCODE"
        }
    }
}

function Get-ExistingTask {
    Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
}

if ($Uninstall) {
    if (Get-ExistingTask) {
        if ($PSCmdlet.ShouldProcess("task '$TaskName'", 'Unregister-ScheduledTask')) {
            Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
            Write-Host "install-task: removed task '$TaskName'"
        }
    } else {
        Write-Host "install-task: task '$TaskName' is not installed; nothing to do"
    }
    Write-Host 'install-task: power settings were left unchanged (Settings > System > Power to change them)'
    return
}

if (-not (Test-Path -LiteralPath $Exe)) {
    if ($WhatIfPreference) {
        Write-Warning "install-task: $Exe does not exist yet; run scripts\build.ps1 before a real install"
    } else {
        throw "install-task: $Exe not found; run scripts\build.ps1 first"
    }
}

# 1. Scheduled task.
$action = New-ScheduledTaskAction -Execute $Exe -Argument 'run' -WorkingDirectory $RepoRoot
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $User
$settings = New-ScheduledTaskSettingsSet `
    -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 5) `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -MultipleInstances IgnoreNew `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable
$principal = New-ScheduledTaskPrincipal -UserId $User -LogonType Interactive -RunLevel Limited

$verb = if (Get-ExistingTask) { 'replace' } else { 'register' }
$desc = "Register-ScheduledTask ($verb): at logon of $User -> `"$Exe`" run (in $RepoRoot); restart 3x every 5 min, no time limit, ignore new instance"
if ($PSCmdlet.ShouldProcess("task '$TaskName'", $desc)) {
    try {
        Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
            -Settings $settings -Principal $principal `
            -Description 'Mayank 2.0 daemon (scripts\install-task.ps1)' -Force | Out-Null
    } catch {
        throw "install-task: could not register task '$TaskName' (try an elevated PowerShell): $($_.Exception.Message)"
    }
    Write-Host "install-task: task '$TaskName' $(if ($verb -eq 'replace') { 'replaced' } else { 'registered' })"
}

# 2. Power settings on AC for the active plan. 0 = never / do nothing.
Invoke-PowerCfg @('/change', 'standby-timeout-ac', '0')
Invoke-PowerCfg @('/change', 'hibernate-timeout-ac', '0')
Invoke-PowerCfg @('/setacvalueindex', 'SCHEME_CURRENT', 'SUB_BUTTONS', 'LIDACTION', '0')
Invoke-PowerCfg @('/setactive', 'SCHEME_CURRENT')
if (-not $WhatIfPreference) {
    Write-Host 'install-task: on AC, sleep = never, hibernate = never, lid close = do nothing'
}

# 3. What Windows cannot set for us.
Write-Host ''
Write-Host 'REMINDER: set the battery charge limit to 80% in your laptop vendor app'
Write-Host '          (it keeps the battery healthy while the laptop stays plugged in).'
