from functools import lru_cache

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    raglab_api_url: str = "http://127.0.0.1:8080"
    deepseek_api_key: str = ""
    raglab_generation_api_key: str = ""
    deepseek_base_url: str = "https://api.deepseek.com"
    deepseek_model: str = "deepseek-chat"
    agent_host: str = "0.0.0.0"
    agent_port: int = 8090
    agent_max_steps: int = 4
    agent_cors_origins: str = "http://localhost:3000,http://127.0.0.1:3000,http://localhost:13000,http://127.0.0.1:13000"

    @property
    def model_api_key(self) -> str:
        return self.deepseek_api_key.strip() or self.raglab_generation_api_key.strip()

    @property
    def cors_origins(self) -> list[str]:
        return [origin.strip() for origin in self.agent_cors_origins.split(",") if origin.strip()]


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    return Settings()
