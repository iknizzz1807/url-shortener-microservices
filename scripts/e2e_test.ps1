$BaseUrl = $env:BASE_URL
if (-not $BaseUrl) {
    $BaseUrl = "http://localhost:8080"
}
$Timestamp = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$Email = "thong-e2e-$Timestamp@example.com"
$Password = "Password123!"
# Helper to perform standard REST request
function Invoke-Request {
    param (
        [string]$Method,
        [string]$Path,
        [string]$Body,
        [string]$Token
    )
    $Method = $Method.ToUpper()
    $headers = @{ "Content-Type" = "application/json" }
    if ($Token) {
        $headers["Authorization"] = "Bearer $Token"
    }
    
    $params = @{
        Method      = $Method
        Uri         = "$BaseUrl$Path"
        Headers     = $headers
        UseBasicParsing = $true
    }
    if ($Body) {
        $params["Body"] = $Body
    }
    
    return Invoke-RestMethod @params
}

# Helper to check HTTP Status Code without throwing on non-200s or redirects
function Get-StatusCode {
    param (
        [string]$Method,
        [string]$Path,
        [string]$Body,
        [string]$Token
    )
    $Method = $Method.ToUpper()
    $uri = "$BaseUrl$Path"
    $request = [System.Net.HttpWebRequest]::Create($uri)
    $request.Method = $Method
    $request.AllowAutoRedirect = $false
    $request.ContentType = "application/json"
    if ($Token) {
        $request.Headers.Add("Authorization", "Bearer $Token")
    }
    if ($Body) {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($Body)
        $request.ContentLength = $bytes.Length
        $requestStream = $request.GetRequestStream()
        $requestStream.Write($bytes, 0, $bytes.Length)
        $requestStream.Close()
    }
    try {
        $response = $request.GetResponse()
        $statusCode = [int]$response.StatusCode
        $response.Close()
        return $statusCode
    } catch [System.Net.WebException] {
        if ($_.Exception.Response) {
            $statusCode = [int]$_.Exception.Response.StatusCode
            $_.Exception.Response.Close()
            return $statusCode
        }
        throw $_
    }
}


Write-Host "1. register"
$RegisterBody = @{ email = $Email; password = $Password } | ConvertTo-Json
$RegisterRes = Invoke-Request -Method Post -Path "/api/auth/register" -Body $RegisterBody
Write-Host "Registration successful."

Write-Host "2. login"
$LoginBody = @{ email = $Email; password = $Password } | ConvertTo-Json
$LoginRes = Invoke-Request -Method Post -Path "/api/auth/login" -Body $LoginBody
$Token = $LoginRes.token
if (-not $Token) {
    Write-Error "Failed to retrieve authentication token."
    exit 1
}
Write-Host "Logged in successfully."

Write-Host "3. shorten"
$ShortenBody = @{ url = "https://example.com/e2e"; expires_in_hours = 24 } | ConvertTo-Json
$ShortenRes = Invoke-Request -Method Post -Path "/api/shorten" -Body $ShortenBody -Token $Token
$ShortCode = $ShortenRes.short_code
if (-not $ShortCode) {
    Write-Error "Failed to retrieve short_code."
    exit 1
}
Write-Host "Short URL created: $BaseUrl/r/$ShortCode"

Write-Host "4. redirect x15"
for ($i = 1; $i -le 15; $i++) {
    $code = Get-StatusCode -Method Get -Path "/r/$ShortCode"
    if ($code -ne 301 -and $code -ne 308) {
        Write-Error "redirect $i returned $code, expected 301 or 308"
        exit 1
    }
}
Write-Host "15 redirects verified."

Write-Host "5. wait for outbox and consumers"
Start-Sleep -Seconds 5

Write-Host "6. stats"
$StatsRes = Invoke-Request -Method Get -Path "/api/stats/$ShortCode"
if ($StatsRes.total_clicks -lt 15) {
    Write-Error "expected at least 15 clicks, got $($StatsRes.total_clicks)"
    exit 1
}
Write-Host "Stats click count verified."

Write-Host "7. notifications"
$NotifRes = Invoke-Request -Method Get -Path "/api/notifications" -Token $Token
$items = $NotifRes.notifications
if (-not $items) {
    $items = $NotifRes.items
}
if (-not $items -or $items.Count -eq 0) {
    Write-Error "expected at least one notification"
    exit 1
}

$hasMilestone = $false
foreach ($item in $items) {
    $eventType = $item.event_type
    if (-not $eventType -and $item.payload) {
        $eventType = $item.payload.event_type
    }
    if ($eventType -eq "milestone.reached") {
        $hasMilestone = $true
        break
    }
}
if (-not $hasMilestone) {
    Write-Error "expected milestone.reached notification"
    exit 1
}
Write-Host "Milestone notification verified."

Write-Host "8. delete"
$deleteCode = Get-StatusCode -Method Delete -Path "/api/urls/$ShortCode" -Token $Token
if ($deleteCode -ne 204) {
    Write-Error "delete returned status $deleteCode, expected 204"
    exit 1
}
Write-Host "URL deleted successfully."

Write-Host "9. deleted redirect returns 410"
$goneCode = Get-StatusCode -Method Get -Path "/r/$ShortCode"
if ($goneCode -ne 410) {
    Write-Error "deleted redirect returned status $goneCode, expected 410"
    exit 1
}
Write-Host "410 Gone status verified."

Write-Host "10. rate limit"
$rateLimited = $false
for ($i = 1; $i -le 11; $i++) {
    $code = Get-StatusCode -Method Post -Path "/api/shorten" -Body $ShortenBody -Token $Token
    if ($code -eq 429) {
        $rateLimited = $true
        break
    }
}
if (-not $rateLimited) {
    Write-Error "expected shorten rate limit to return 429"
    exit 1
}
Write-Host "Rate limit (429) verified."

Write-Host "11. correlation header"
$headers = @{}
try {
    $response = Invoke-WebRequest -Method Get -Uri "$BaseUrl/health" -Headers @{ "Content-Type" = "application/json" } -UseBasicParsing
    $headers = $response.Headers
} catch {
    if ($_.Exception.Response) {
        $headers = $_.Exception.Response.Headers
    }
}
$correlation = $headers["X-Correlation-ID"]
if (-not $correlation) {
    Write-Error "missing X-Correlation-ID header"
    exit 1
}
Write-Host "Correlation ID ($correlation) verified."

Write-Host "E2E passed"
