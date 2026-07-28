namespace OrionBelt.SDK;

public sealed class ApiError : Exception
{
    public int StatusCode { get; }
    public string Body { get; }

    public ApiError(int statusCode, string message, string body) : base(string.IsNullOrWhiteSpace(message) ? $"api error ({statusCode})" : message)
    {
        StatusCode = statusCode;
        Body = body;
    }
}