<#
  Child-process harness for install-task.tests.ps1: runs install-task.ps1 with
  -WhatIf while every state-changing command is shadowed by a global function
  that prints HARNESS-CALLED and exits. The ScheduledTasks module is imported
  first so its autoload cannot replace the shadows. Get-ScheduledTask is
  shadowed too, so the real Task Scheduler is never queried.
#>
param(
    [Parameter(Mandatory = $true)][string]$Script,
    [string]$RepoRoot = '',
    [switch]$Uninstall,
    [switch]$TaskExists
)
$ErrorActionPreference = 'Stop'
Import-Module ScheduledTasks
$global:M2HarnessTaskExists = [bool]$TaskExists

function global:Get-ScheduledTask {
    param([string]$TaskName, $ErrorAction)
    if ($global:M2HarnessTaskExists) { [pscustomobject]@{ TaskName = $TaskName } }
}
function global:Register-ScheduledTask { Write-Output 'HARNESS-CALLED Register-ScheduledTask'; exit 3 }
function global:Unregister-ScheduledTask { Write-Output 'HARNESS-CALLED Unregister-ScheduledTask'; exit 3 }
function global:powercfg { Write-Output "HARNESS-CALLED powercfg $args"; exit 3 }

foreach ($n in 'Get-ScheduledTask', 'Register-ScheduledTask', 'Unregister-ScheduledTask', 'powercfg') {
    $c = Get-Command $n
    if ($c.CommandType -ne 'Function' -or $c.ModuleName) { Write-Output "ABORT: '$n' is not shadowed"; exit 2 }
}

if ($RepoRoot) { & $Script -RepoRoot $RepoRoot -Uninstall:$Uninstall -WhatIf }
else { & $Script -Uninstall:$Uninstall -WhatIf }
exit 0
