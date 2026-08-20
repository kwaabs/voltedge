# auth-smoke.ps1 -- Phase 10 Slice A manual smoke test.
#
# Runs the full local-login flow against a locally-running API
# (task api-dev, listening on http://localhost:3704):
#   login -> me -> flip the local-auth switch -> logout -> me (expect 401)
#
# Usage (from the repo root, with the API already running):
#   powershell -ExecutionPolicy Bypass -File .\scripts\auth-smoke.ps1
#
# Edit $Email / $Password below to match the account you created with
# `task authadmin-create`. Requires AUTH_INSECURE_COOKIES=1 in .env so the
# session cookies drop their Secure flag over plain http and actually stick.

$ErrorActionPreference = "Stop"

$BaseUrl  = "http://localhost:3704"
$Email    = "admin@yourutility.com"
$Password = "choose-a-strong-one"   # <-- the password you set at create time

# One session object carries the cookies across every request.
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession

Write-Host "== 1. login ==" -ForegroundColor Cyan
$loginBody = @{ email = $Email; password = $Password } | ConvertTo-Json
$login = Invoke-RestMethod -Uri "$BaseUrl/auth/login" -Method Post `
    -ContentType "application/json" -Body $loginBody -WebSession $session
Write-Host "logged in as:" ($login | ConvertTo-Json -Compress)

Write-Host "`n== 2. cookies received ==" -ForegroundColor Cyan
$cookies = $session.Cookies.GetCookies($BaseUrl)
foreach ($c in $cookies) { Write-Host ("  {0} = {1}" -f $c.Name, $c.Value) }

$csrfCookie = $cookies["ve_csrf"]
if (-not $csrfCookie) { throw "ve_csrf cookie not set -- is AUTH_INSECURE_COOKIES=1 in .env? (Secure cookies won't stick over http)" }
$csrf = $csrfCookie.Value

Write-Host "`n== 3. GET /auth/me ==" -ForegroundColor Cyan
$me = Invoke-RestMethod -Uri "$BaseUrl/auth/me" -Method Get -WebSession $session
Write-Host "me:" ($me | ConvertTo-Json -Compress)

Write-Host "`n== 4. flip local-auth switch on ==" -ForegroundColor Cyan
$settingsBody = @{ enabled = $true } | ConvertTo-Json
$setResp = Invoke-RestMethod -Uri "$BaseUrl/auth/settings/local-auth" -Method Post `
    -ContentType "application/json" -Body $settingsBody `
    -Headers @{ "X-CSRF-Token" = $csrf } -WebSession $session
Write-Host "switch now:" ($setResp | ConvertTo-Json -Compress)

Write-Host "`n== 5. logout ==" -ForegroundColor Cyan
Invoke-WebRequest -Uri "$BaseUrl/auth/logout" -Method Post -UseBasicParsing `
    -Headers @{ "X-CSRF-Token" = $csrf } -WebSession $session | Out-Null
Write-Host "logged out (204 expected)"

Write-Host "`n== 6. GET /auth/me after logout (expect 401) ==" -ForegroundColor Cyan
try {
    Invoke-RestMethod -Uri "$BaseUrl/auth/me" -Method Get -WebSession $session | Out-Null
    Write-Host "UNEXPECTED: /auth/me still succeeded after logout" -ForegroundColor Red
} catch {
    $code = $_.Exception.Response.StatusCode.value__
    if ($code -eq 401) { Write-Host "got 401 as expected -- session revoked." -ForegroundColor Green }
    else { Write-Host "got $code (expected 401)" -ForegroundColor Yellow }
}

Write-Host "`nDone." -ForegroundColor Green
