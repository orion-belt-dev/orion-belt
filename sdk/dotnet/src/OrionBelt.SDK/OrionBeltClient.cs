using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;

namespace OrionBelt.SDK;

public sealed class OrionBeltClient
{
    private readonly HttpClient _httpClient;
    private readonly string _baseUrl;
    private string? _apiKey;
    private string? _sessionToken;
    private string? _bearerToken;

    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);

    public OrionBeltClient(string baseUrl, HttpClient? httpClient = null, string? apiKey = null, string? sessionToken = null, string? bearerToken = null)
    {
        if (string.IsNullOrWhiteSpace(baseUrl))
        {
            throw new ArgumentException("baseUrl is required", nameof(baseUrl));
        }

        _baseUrl = baseUrl.Trim().TrimEnd('/');
        _httpClient = httpClient ?? new HttpClient { Timeout = TimeSpan.FromSeconds(30) };
        _apiKey = string.IsNullOrWhiteSpace(apiKey) ? null : apiKey.Trim();
        _sessionToken = string.IsNullOrWhiteSpace(sessionToken) ? null : sessionToken.Trim();
        _bearerToken = string.IsNullOrWhiteSpace(bearerToken) ? null : bearerToken.Trim();
    }

    public void SetApiKey(string? apiKey) => _apiKey = string.IsNullOrWhiteSpace(apiKey) ? null : apiKey.Trim();
    public void SetSessionToken(string? sessionToken) => _sessionToken = string.IsNullOrWhiteSpace(sessionToken) ? null : sessionToken.Trim();
    public void SetBearerToken(string? bearerToken) => _bearerToken = string.IsNullOrWhiteSpace(bearerToken) ? null : bearerToken.Trim();

    private HttpRequestMessage CreateRequest(HttpMethod method, string path, bool auth = true)
    {
        var normalized = path.StartsWith("http://", StringComparison.OrdinalIgnoreCase) || path.StartsWith("https://", StringComparison.OrdinalIgnoreCase)
            ? path
            : $"{_baseUrl}{(path.StartsWith("/api/") ? "" : "/api/v1")}{(path.StartsWith('/') ? path : "/" + path)}";

        var request = new HttpRequestMessage(method, normalized);
        if (auth)
        {
            if (_apiKey is not null)
            {
                request.Headers.TryAddWithoutValidation("X-API-Key", _apiKey);
            }
            else if (_sessionToken is not null)
            {
                request.Headers.TryAddWithoutValidation("X-Session-Token", _sessionToken);
            }

            if (_bearerToken is not null)
            {
                request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", _bearerToken);
            }
        }

        return request;
    }

    private async Task<T> SendJsonAsync<T>(HttpMethod method, string path, object? body = null, bool auth = true, CancellationToken cancellationToken = default)
    {
        using var request = CreateRequest(method, path, auth);
        if (body is not null)
        {
            request.Content = new StringContent(JsonSerializer.Serialize(body, JsonOptions), Encoding.UTF8, "application/json");
        }

        using var response = await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        var payload = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);

        if (!response.IsSuccessStatusCode)
        {
            throw new ApiError((int)response.StatusCode, ExtractErrorMessage(payload), payload);
        }

        if (string.IsNullOrWhiteSpace(payload))
        {
            return default!;
        }

        return JsonSerializer.Deserialize<T>(payload, JsonOptions)!;
    }

    private async Task<byte[]> SendBytesAsync(HttpMethod method, string path, bool auth = true, CancellationToken cancellationToken = default)
    {
        using var request = CreateRequest(method, path, auth);
        using var response = await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        var payload = await response.Content.ReadAsByteArrayAsync(cancellationToken).ConfigureAwait(false);

        if (!response.IsSuccessStatusCode)
        {
            throw new ApiError((int)response.StatusCode, Encoding.UTF8.GetString(payload).Trim(), Encoding.UTF8.GetString(payload));
        }

        return payload;
    }

    private static string ExtractErrorMessage(string payload)
    {
        if (string.IsNullOrWhiteSpace(payload))
        {
            return string.Empty;
        }

        try
        {
            using var doc = JsonDocument.Parse(payload);
            if (doc.RootElement.TryGetProperty("error", out var error) && error.ValueKind == JsonValueKind.String)
            {
                return error.GetString() ?? string.Empty;
            }

            if (doc.RootElement.TryGetProperty("message", out var message) && message.ValueKind == JsonValueKind.String)
            {
                return message.GetString() ?? string.Empty;
            }
        }
        catch (JsonException)
        {
        }

        return payload.Trim();
    }

    public Task<string> IssueChallengeAsync(string username, CancellationToken cancellationToken = default)
        => SendJsonAsync<ChallengeResponse>(HttpMethod.Post, "/public/auth/challenge", new { username }, auth: false, cancellationToken).ContinueWith(t => t.Result.Challenge, cancellationToken);

    public async Task<LoginResponse> LoginWithPasswordAsync(string username, string password, string totpCode, CancellationToken cancellationToken = default)
    {
        var result = await SendJsonAsync<LoginResponse>(HttpMethod.Post, "/public/login/password", new { username, password, totp_code = totpCode }, auth: false, cancellationToken).ConfigureAwait(false);
        _sessionToken = result.SessionToken;
        if (!string.IsNullOrWhiteSpace(result.AccessToken))
        {
            _bearerToken = result.AccessToken;
        }
        return result;
    }

    public async Task<LoginWithKeyResponse> LoginWithKeyAsync(object payload, CancellationToken cancellationToken = default)
    {
        var result = await SendJsonAsync<LoginWithKeyResponse>(HttpMethod.Post, "/public/login/key", payload, false, cancellationToken).ConfigureAwait(false);
        _apiKey = result.ApiKey;
        return result;
    }

    public async Task<JWTLoginResponse> LoginWithJwtAsync(object payload, CancellationToken cancellationToken = default)
    {
        var result = await SendJsonAsync<JWTLoginResponse>(HttpMethod.Post, "/public/login/token", payload, false, cancellationToken).ConfigureAwait(false);
        _bearerToken = result.AccessToken;
        return result;
    }

    public Task<AuthMe> GetCurrentUserAsync(CancellationToken cancellationToken = default)
        => SendJsonAsync<AuthMe>(HttpMethod.Get, "/auth/me", null, true, cancellationToken);

    public Task<Machine[]> ListMachinesAsync(CancellationToken cancellationToken = default)
        => SendJsonAsync<Machine[]>(HttpMethod.Get, "/machines", null, true, cancellationToken);

    public async Task<UsageDashboard> GetUsageDashboardAsync(int windowHours = 24, int top = 10, CancellationToken cancellationToken = default)
    {
        var path = $"/dashboard/usage?window_hours={windowHours}&top={top}";
        return await SendJsonAsync<UsageDashboard>(HttpMethod.Get, path, null, true, cancellationToken).ConfigureAwait(false);
    }

    public async Task<byte[]> ExportReportAsync(string reportName, string format = "csv", CancellationToken cancellationToken = default)
    {
        var path = $"/reports/{Uri.EscapeDataString(reportName)}/export?format={Uri.EscapeDataString(format)}";
        return await SendBytesAsync(HttpMethod.Get, path, true, cancellationToken).ConfigureAwait(false);
    }

    public async Task<FileListResponse> ListFilesAsync(string machine, string path = "/", string remoteUser = "root", CancellationToken cancellationToken = default)
    {
        var query = $"machine={Uri.EscapeDataString(machine)}&path={Uri.EscapeDataString(path)}&user={Uri.EscapeDataString(remoteUser)}";
        var payload = await SendJsonAsync<JsonElement>(HttpMethod.Get, $"/files/list?{query}", null, true, cancellationToken).ConfigureAwait(false);
        var entries = new List<FileEntry>();
        if (payload.TryGetProperty("entries", out var entriesElement) && entriesElement.ValueKind == JsonValueKind.Array)
        {
            foreach (var item in entriesElement.EnumerateArray())
            {
                entries.Add(JsonSerializer.Deserialize<FileEntry>(item.GetRawText(), JsonOptions)!);
            }
        }

        return new FileListResponse(
            payload.GetProperty("path").GetString() ?? string.Empty,
            entries,
            payload.TryGetProperty("raw", out var raw) && raw.ValueKind == JsonValueKind.String ? raw.GetString() : null);
    }

    public Task<byte[]> DownloadFileAsync(string machine, string path, string remoteUser = "root", CancellationToken cancellationToken = default)
    {
        var query = $"machine={Uri.EscapeDataString(machine)}&path={Uri.EscapeDataString(path)}&user={Uri.EscapeDataString(remoteUser)}";
        return SendBytesAsync(HttpMethod.Get, $"/files/download?{query}", true, cancellationToken);
    }

    public async Task<FileUploadResponse> UploadFileAsync(string machine, string path, string fileName, byte[] content, string remoteUser = "root", CancellationToken cancellationToken = default)
    {
        using var form = new MultipartFormDataContent();
        form.Add(new StringContent(machine), "machine");
        form.Add(new StringContent(path), "path");
        form.Add(new StringContent(remoteUser), "user");
        form.Add(new ByteArrayContent(content), "file", fileName);

        using var request = CreateRequest(HttpMethod.Post, "/files/upload", auth: true);
        request.Content = form;

        using var response = await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        var payload = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
        if (!response.IsSuccessStatusCode)
        {
            throw new ApiError((int)response.StatusCode, ExtractErrorMessage(payload), payload);
        }

        return JsonSerializer.Deserialize<FileUploadResponse>(payload, JsonOptions)!;
    }
}