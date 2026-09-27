<#
.SYNOPSIS
  Tests for scripts\install-task.ps1. Nothing here touches the real Task
  Scheduler or power plan: Register/Unregister/Get-ScheduledTask and powercfg
  are shadowed by recording global functions. The ScheduledTasks module is
  imported FIRST and the shadows are defined after it in the global scope:
  otherwise the module's autoload (triggered by New-ScheduledTaskAction) wins
  over script-scope functions and the real cmdlets run. A guard checks every
  shadow resolves before any case runs, and the real task list is checked
  (read-only, module-qualified) before and after. The -WhatIf cases run in a
  child process through whatif-harness.ps1, whose shadows fail if called.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts\tests\install-task.tests.ps1
#>
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Script = Join-Path (Split-Path -Parent $PSScriptRoot) 'install-task.ps1'
$Harness = Join-Path $PSScriptRoot 'whatif-harness.ps1'

# --- fixture repo root with a fake exe --------------------------------------
$Fixture = Join-Path ([IO.Path]::GetTempPath()) ("m2-install-task-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path (Join-Path $Fixture 'bin') | Out-Null
Set-Content -LiteralPath (Join-Path $Fixture 'bin\mayank2.exe') -Value 'fake'
$NoExe = Join-Path $Fixture 'empty'
New-Item -ItemType Directory -Path $NoExe | Out-Null

# --- recording shadows ------------------------------------------------------
Import-Module ScheduledTasks
function Test-RealTaskExists { [bool](ScheduledTasks\Get-ScheduledTask -TaskName 'Mayank2' -ErrorAction SilentlyContinue) }
$RealTaskBefore = Test-RealTaskExists

function Reset-Mocks([bool]$TaskExists = $false) {
    $global:M2TaskExists = $TaskExists
    $global:M2Calls = New-Object System.Collections.ArrayList
}
function global:Get-ScheduledTask {
    param([string]$TaskName, $ErrorAction)
    if ($global:M2TaskExists) { [pscustomobject]@{ TaskName = $TaskName } }
}
function global:Register-ScheduledTask {
    param([string]$TaskName, $Action, $Trigger, $Settings, $Principal, [string]$Description, [switch]$Force)
    [void]$global:M2Calls.Add([pscustomobject]@{ Cmd = 'register'; TaskName = $TaskName; Action = $Action;
            Trigger = $Trigger; Settings = $Settings; Principal = $Principal; Force = [bool]$Force })
    $global:M2TaskExists = $true
    [pscustomobject]@{ TaskName = $TaskName }
}
function global:Unregister-ScheduledTask {
    param([string]$TaskName, [switch]$Confirm)
    [void]$global:M2Calls.Add([pscustomobject]@{ Cmd = 'unregister'; TaskName = $TaskName })
    $global:M2TaskExists = $false
}
function global:powercfg {
    [void]$global:M2Calls.Add([pscustomobject]@{ Cmd = 'powercfg'; Args = ($args -join ' ') })
    $global:LASTEXITCODE = 0
}

# Guard: refuse to run if any shadow would not be used (e.g. module loaded later).
& {
    $null = New-ScheduledTaskAction -Execute 'x'
    foreach ($n in 'Get-ScheduledTask', 'Register-ScheduledTask', 'Unregister-ScheduledTask', 'powercfg') {
        $c = Get-Command $n
        if ($c.CommandType -ne 'Function' -or $c.ModuleName) {
            Write-Host "ABORT: '$n' resolves to $($c.CommandType) from '$($c.ModuleName)', not the test shadow"
            exit 2
        }
    }
}

# --- tiny assertion helpers -------------------------------------------------
$script:Failures = 0
$script:Passes = 0
function Assert-Equal($Expected, $Actual, [string]$What) {
    if ("$Expected" -ne "$Actual") {
        Write-Host "    FAIL $What`n         expected: $Expected`n         actual:   $Actual"
        $script:Failures++
    } else { $script:Passes++ }
}
function Assert-True([bool]$Cond, [string]$What) {
    if (-not $Cond) { Write-Host "    FAIL $What"; $script:Failures++ } else { $script:Passes++ }
}
function Test-Case([string]$Name, [scriptblock]$Body) {
    Write-Host "--- $Name"
    $before = $script:Failures
    try { & $Body } catch { Write-Host "    FAIL threw: $($_.Exception.Message)"; $script:Failures++ }
    if ($script:Failures -eq $before) { Write-Host '    ok' }
}
function Get-Calls([string]$Cmd) { ,@($global:M2Calls | Where-Object { $_.Cmd -eq $Cmd }) }

$ExpectedPower = @(
    '/change standby-timeout-ac 0',
    '/change hibernate-timeout-ac 0',
    '/setacvalueindex SCHEME_CURRENT SUB_BUTTONS LIDACTION 0',
    '/setactive SCHEME_CURRENT'
)

# --- cases ------------------------------------------------------------------
try {
    Test-Case 'install registers the task with the required settings' {
        Reset-Mocks
        $out = & $Script -RepoRoot $Fixture 6>&1 | Out-String
        $reg = Get-Calls 'register'
        Assert-Equal 1 $reg.Count 'one Register-ScheduledTask call'
        $r = $reg[0]
        Assert-Equal 'Mayank2' $r.TaskName 'task name'
        Assert-True $r.Force 'registered with -Force (replace in place)'
        Assert-Equal (Join-Path $Fixture 'bin\mayank2.exe') $r.Action.Execute 'action exe'
        Assert-Equal 'run' $r.Action.Arguments 'action arguments'
        Assert-Equal $Fixture $r.Action.WorkingDirectory 'working directory = repo root'
        Assert-Equal 'MSFT_TaskLogonTrigger' $r.Trigger.CimClass.CimClassName 'trigger is at logon'
        $user = if ($env:USERDOMAIN) { "$env:USERDOMAIN\$env:USERNAME" } else { $env:USERNAME }
        Assert-Equal $user $r.Trigger.UserId 'logon trigger is for the current user'
        Assert-Equal $user $r.Principal.UserId 'runs as the current user'
        Assert-Equal 'Limited' $r.Principal.RunLevel 'not elevated'
        Assert-Equal 3 $r.Settings.RestartCount 'restart count'
        Assert-Equal 'PT5M' $r.Settings.RestartInterval 'restart interval 5 min'
        Assert-Equal 'PT0S' $r.Settings.ExecutionTimeLimit 'no execution time limit'
        Assert-Equal 'IgnoreNew' $r.Settings.MultipleInstances 'ignore new instance'
        Assert-Equal ($ExpectedPower -join '|') ((Get-Calls 'powercfg').Args -join '|') 'powercfg calls'
        Assert-True ($out -match 'battery charge limit to 80%') 'prints the 80% battery reminder'
    }

    Test-Case 'second install is idempotent (replaces, same calls)' {
        Reset-Mocks -TaskExists $true
        $out = & $Script -RepoRoot $Fixture 6>&1 | Out-String
        Assert-Equal 1 (Get-Calls 'register').Count 'one Register-ScheduledTask call'
        Assert-Equal 0 (Get-Calls 'unregister').Count 'no unregister on re-install'
        Assert-Equal ($ExpectedPower -join '|') ((Get-Calls 'powercfg').Args -join '|') 'same powercfg calls'
        Assert-True ($out -match "task 'Mayank2' replaced") 'reports replace'
    }

    Test-Case 'install without bin\mayank2.exe fails before changing anything' {
        Reset-Mocks
        $threw = $false
        try { & $Script -RepoRoot $NoExe 6>$null } catch { $threw = $_.Exception.Message -match 'build\.ps1' }
        Assert-True $threw 'throws and points at build.ps1'
        Assert-Equal 0 $global:M2Calls.Count 'no calls made'
    }

    Test-Case '-Uninstall removes an installed task and leaves power alone' {
        Reset-Mocks -TaskExists $true
        & $Script -RepoRoot $Fixture -Uninstall 6>$null
        $un = Get-Calls 'unregister'
        Assert-Equal 1 $un.Count 'one Unregister-ScheduledTask call'
        Assert-Equal 'Mayank2' $un[0].TaskName 'task name'
        Assert-Equal 0 (Get-Calls 'powercfg').Count 'no powercfg calls'
        Assert-Equal 0 (Get-Calls 'register').Count 'no register'
    }

    Test-Case '-Uninstall when not installed is a no-op' {
        Reset-Mocks
        $out = & $Script -RepoRoot $Fixture -Uninstall 6>&1 | Out-String
        Assert-Equal 0 $global:M2Calls.Count 'no calls made'
        Assert-True ($out -match 'not installed') 'says nothing to do'
    }

    Test-Case '-WhatIf prints every action and calls nothing (child process)' {
        $out = & powershell -NoProfile -ExecutionPolicy Bypass -File $Harness -Script $Script -RepoRoot $Fixture 2>&1 | Out-String
        Assert-Equal 0 $LASTEXITCODE "harness exit code (output: $out)"
        Assert-True ($out -match 'What if: .*Register-ScheduledTask \(register\): at logon of .* run \(in ') 'what-if for the task'
        Assert-True ($out -match 'restart 3x every 5 min, no time limit, ignore new instance') 'what-if lists task settings'
        foreach ($p in $ExpectedPower) {
            Assert-True ($out -match [regex]::Escape("powercfg $p")) "what-if for powercfg $p"
        }
        Assert-True ($out -match 'battery charge limit to 80%') 'what-if still prints the reminder'
        Assert-True ($out -notmatch 'HARNESS-CALLED') 'no real action was called'
    }

    Test-Case '-WhatIf -Uninstall calls nothing (child process)' {
        $out = & powershell -NoProfile -ExecutionPolicy Bypass -File $Harness -Script $Script -RepoRoot $Fixture -Uninstall -TaskExists 2>&1 | Out-String
        Assert-Equal 0 $LASTEXITCODE "harness exit code (output: $out)"
        Assert-True ($out -match "What if: .*Unregister-ScheduledTask.*task 'Mayank2'") 'what-if for unregister'
        Assert-True ($out -notmatch 'HARNESS-CALLED') 'no real action was called'
    }

    # Default -RepoRoot = parent of the script's folder. Use a copy of the
    # script inside the fixture so the expected root is the fixture.
    $copyDir = Join-Path $Fixture 'scripts'
    New-Item -ItemType Directory -Path $copyDir | Out-Null
    $copy = Join-Path $copyDir 'install-task.ps1'
    Copy-Item -LiteralPath $Script -Destination $copy

    Test-Case 'default repo root is the parent of scripts\ (child process, -WhatIf)' {
        $out = & powershell -NoProfile -ExecutionPolicy Bypass -File $Harness -Script $copy 2>&1 | Out-String
        Assert-Equal 0 $LASTEXITCODE "harness exit code (output: $out)"
        Assert-True ($out -match [regex]::Escape("(in $Fixture)")) 'working dir is the fixture root'
        Assert-True ($out -notmatch 'HARNESS-CALLED') 'no real action was called'
    }

    # The exact user path: powershell -File install-task.ps1 -WhatIf, no shadows.
    # Only run once every shadowed case above proved -WhatIf changes nothing.
    if ($script:Failures -eq 0) {
        Test-Case 'powershell -File install-task.ps1 -WhatIf (real, read-only)' {
            $out = & powershell -NoProfile -ExecutionPolicy Bypass -File $copy -WhatIf 2>&1 | Out-String
            Assert-Equal 0 $LASTEXITCODE "exit code (output: $out)"
            Assert-True ($out -match [regex]::Escape("(in $Fixture)")) 'working dir is the fixture root'
            Assert-True ($out -match 'What if: .*powercfg /setactive SCHEME_CURRENT') 'what-if for power'
        }
    }
} finally {
    Remove-Item -LiteralPath $Fixture -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host '--- the real Task Scheduler was not changed'
Assert-Equal $RealTaskBefore (Test-RealTaskExists) "real task 'Mayank2' present before/after"

Write-Host ''
Write-Host "install-task tests: $script:Passes assertions passed, $script:Failures failed"
if ($script:Failures -gt 0) { exit 1 }
exit 0
