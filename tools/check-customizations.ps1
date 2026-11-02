param([switch]$Full)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

function Invoke-Checked {
    param([string]$Directory, [string]$Program, [string[]]$Arguments)
    Push-Location -LiteralPath $Directory
    try {
        Write-Host "$Program $($Arguments -join ' ')"
        & $Program @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$Program failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}

if ($env:OS -eq 'Windows_NT') {
    $git = Get-Command git -ErrorAction Stop
    $gitRoot = Split-Path -Parent (Split-Path -Parent $git.Source)
    $posixTools = Join-Path $gitRoot 'usr\bin'
    if (Test-Path -LiteralPath (Join-Path $posixTools 'sh.exe')) {
        $env:PATH = "$posixTools$([IO.Path]::PathSeparator)$env:PATH"
    }
}

$backend = Join-Path $root 'backend'
$frontend = Join-Path $root 'frontend'
$goArguments = @('test', '-tags=unit')
if (-not $Full) {
    $goArguments += @('-run', 'Test.*(DailyQuota|FiveHour|AccountQuota|ModelEvaluation|EvaluationProxy|AccountEvaluation|LocalQuota|AccountCustomization|ScopedAdmin.*Quota)')
}
$goArguments += './...'
Invoke-Checked -Directory $backend -Program 'go' -Arguments $goArguments

Invoke-Checked -Directory $frontend -Program 'npx' -Arguments @('--yes', 'pnpm@9', 'run', 'lint:check')
Invoke-Checked -Directory $frontend -Program 'npx' -Arguments @('--yes', 'pnpm@9', 'run', 'typecheck')

$testArguments = @('--yes', 'pnpm@9', 'exec', 'vitest', 'run')
if (-not $Full) {
    $testArguments += @(
        'src/api/__tests__/admin.accountEvaluation.spec.ts',
        'src/api/__tests__/admin.accountEvaluationHistory.spec.ts',
        'src/api/__tests__/channelMonitorCatalog.spec.ts',
        'src/components/account/__tests__/AccountCapacityCell.spec.ts',
        'src/components/account/__tests__/AccountUsageCell.spec.ts',
        'src/components/admin/account/__tests__',
        'src/components/user/monitor/__tests__',
        'src/composables/__tests__/useAccountEvaluationModel.spec.ts',
        'src/features/channel-monitor-v2/__tests__/designSystem.structure.spec.ts',
        'src/utils/__tests__/accountLocalQuota.spec.ts',
        'src/utils/__tests__/accountQuota.spec.ts',
        'src/utils/__tests__/concurrentMap.spec.ts',
        'src/views/admin/__tests__/AccountsView.lite.spec.ts',
        'src/views/admin/__tests__/AccountEvaluationView.spec.ts',
        'src/views/admin/__tests__/OpenAIDailyQuotaView.spec.ts',
        'src/views/user/__tests__/ChannelStatusV2View.catalog.spec.ts'
    )
}
Invoke-Checked -Directory $frontend -Program 'npx' -Arguments $testArguments
Invoke-Checked -Directory $frontend -Program 'npx' -Arguments @('--yes', 'pnpm@9', 'run', 'build')
Write-Host 'Customization checks passed.'
