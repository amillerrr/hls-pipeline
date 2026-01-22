resource "aws_media_convert_queue" "drm_queue" {
  name   = "hls-drm-queue"
  status = "ACTIVE"

  pricing_plan = "ON_DEMAND"
}

# SPEKE Key Provider (e.g., BuyDRM, PallyCon, Axinom)
resource "aws_api_gateway_rest_api" "speke_proxy" {
  name        = "hls-speke-proxy"
  description = "SPEKE key exchange proxy"
}
