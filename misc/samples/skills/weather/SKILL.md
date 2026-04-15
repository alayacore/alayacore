---
name: weather
description: Use this skill whenever the user wants to get weather information. This includes current weather, forecasts, temperature, humidity, wind, and weather conditions for any city or region.
---

# Weather Skill

Get weather information using the weather script.

## Usage

```sh
./scripts/weather.sh "City name"
```

- **Note**: Use English or Pinyin for city names (e.g. Use "Wuhan" instead of "武汉")

The script fetches weather data from wttr.in in JSON format.

## Output

Current: `location | temp°C, condition | Humidity: X% | Wind: Ykm/h`
Forecast: `date | condition | temp°C`

## Example

```sh
./scripts/weather.sh "New York"
```

Output:
```
New York | 18°C, Partly cloudy | Humidity: 65% | Wind: 12km/h
2026-02-23 | Partly Cloudy | 18°C
2026-02-24 | Sunny | 20°C
2026-02-25 | Light Rain | 16°C
```
