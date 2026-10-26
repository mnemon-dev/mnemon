# mnemon ZCode SessionStart hook for Windows PowerShell.

# ZCode sends UTF-8 JSON regardless of the Windows console code page, and the
# hook must answer in UTF-8: stdout defaults to the ANSI code page, which would
# re-encode non-ASCII guide text on the way out.
[Console]::InputEncoding = [Text.Encoding]::UTF8
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$null = [Console]::In.ReadToEnd()

function Resolve-Mnemon {
    if ($env:MNEMON_BIN -and (Test-Path -LiteralPath $env:MNEMON_BIN -PathType Leaf)) {
        return $env:MNEMON_BIN
    }

    $command = Get-Command mnemon -ErrorAction SilentlyContinue
    if ($null -ne $command) {
        return $command.Source
    }

    $fallback = Join-Path $HOME "go\bin\mnemon.exe"
    if (Test-Path -LiteralPath $fallback -PathType Leaf) {
        return $fallback
    }

    return $null
}

$dataRoot = if ($env:MNEMON_DATA_DIR) { $env:MNEMON_DATA_DIR } else { Join-Path $HOME ".mnemon" }
$promptDir = Join-Path $dataRoot "prompt"
$guidePath = Join-Path $promptDir "guide.md"
$fallbackGuidePath = Join-Path (Join-Path $HOME ".mnemon") "prompt\guide.md"
if (!(Test-Path -LiteralPath $guidePath -PathType Leaf) -and (Test-Path -LiteralPath $fallbackGuidePath -PathType Leaf)) {
    $guidePath = $fallbackGuidePath
}

$context = [System.Collections.Generic.List[string]]::new()
$mnemon = Resolve-Mnemon
if ($null -ne $mnemon) {
    $statusText = (& $mnemon status 2>$null | Out-String).Trim()
    if ($statusText) {
        try {
            $status = $statusText | ConvertFrom-Json
            $insights = if ($null -ne $status.total_insights) { $status.total_insights } else { 0 }
            $edges = if ($null -ne $status.edge_count) { $status.edge_count } else { 0 }
            $context.Add("[mnemon] Memory active ($insights insights, $edges edges).")
        } catch {
            $context.Add("[mnemon] Memory active.")
        }
    } else {
        $context.Add("[mnemon] Memory active.")
    }
} else {
    $context.Add("[mnemon] Warning: mnemon not found in PATH. Set MNEMON_BIN or add mnemon to PATH.")
}

if (Test-Path -LiteralPath $guidePath -PathType Leaf) {
    # guide.md is UTF-8 without BOM; PowerShell 5.1 defaults to the ANSI code
    # page and would corrupt non-ASCII characters in the injected context.
    $context.Add((Get-Content -LiteralPath $guidePath -Raw -Encoding UTF8))
}

$text = ($context -join "`n").Trim()
if ($text) {
    [ordered]@{
        hookSpecificOutput = [ordered]@{
            hookEventName = "SessionStart"
            additionalContext = $text
        }
    } | ConvertTo-Json -Compress -Depth 4
} else {
    "{}"
}
