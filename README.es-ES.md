

# Gastro

[![Go Reference](https://pkg.go.dev/badge/github.com/andrioid/gastro.svg)](https://pkg.go.dev/github.com/andrioid/gastro)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Un framework de componentes basado en archivos para Go. Los archivos `.gastro` combinan frontmatter de Go
con marcado `html/template` en un solo archivo, compilado a código Go seguro en tipos con
enrutamiento basado en archivos. Prueba.

Pensado como: la experiencia de desarrollador de Astro, la seguridad de tipos de Go, el enrutamiento basado en archivos de PHP.

**Estado:** Desarrollo inicial. El pipeline central funciona de extremo a extremo (análisis, generación de código,
enrutamiento, compilación, servicio). El soporte para editores está en progreso.

Consulta [`ROADMAP.md`](ROADMAP.md) para trabajo diferido y limitaciones conocidas, y [`DECISIONS.md`](DECISIONS.md) para el registro cronológico de diseño.


## Alcance

### Objetivos

- Hacer que el desarrollo web con Go sea divertido de nuevo
- Go idiomático: `html/template`, `net/http` y `go:embed` estándar bajo el capó. Sin un nuevo lenguaje que aprender.
- Seguridad de tipos para páginas, componentes y plantillas
- Estructura de proyecto con opiniones propias

### No objetivos

- La arquitectura de islas de Astro es genial, pero está fuera del alcance de este proyecto. Nos centramos en la renderización del servidor.
- Análisis/empaquetado de CSS/estilos desde archivos .gastro
- Análisis/empaquetado de JS desde archivos .gastro



## Inicio Rápido

Gastro se adapta a dos tipos de proyectos. Elige el que mejor se ajuste a ti:

### Modo framework: construir un sitio nuevo

Para proyectos desde cero donde gastro prepara la estructura, controla el servidor de desarrollo
y maneja la compilación:

```sh
# install (pick one):
mise use github:andrioid/gastro@latest
# or: go install github.com/andrioid/gastro/cmd/gastro@latest

gastro new myapp && cd myapp
gastro dev
```

Abre [http://localhost:4242](http://localhost:4242) y edita
`pages/index.gastro`. Guía completa: [Inicio Rápido — Modo Framework](docs/getting-started.md).

### Modo biblioteca: agregar gastro a un proyecto Go existente

Para servicios Go existentes que incorporan una interfaz de usuario: herramientas administrativas, paneles, páginas de estado,
marketing renderizado en el servidor junto a una API:

```sh
cd your-project
go get -tool github.com/andrioid/gastro/cmd/gastro
mkdir -p internal/web/{pages,components}

# Smallest integration: render one component into an existing handler.
# OR wire gastro.New(...) into your main.go for the full pattern.

gastro watch --run 'go run ./cmd/myapp'
```

Guía completa: [Inicio Rápido — Modo Biblioteca](docs/getting-started-library.md).

El **tiempo de ejecución es idéntico entre modos**. Lo que difiere es el
inicio (`gastro new` vs configuración manual), la estructura (preparada vs tu
árbol existente) y el comando del ciclo de desarrollo (`gastro dev` vs
`gastro watch`).

### Requisitos previos

- Go 1.26+
- Uno de: [mise](https://mise.jdx.dev/), `go install` o `go get -tool`

### Todos los métodos de instalación

Tres formas equivalentes de obtener la CLI de gastro. El modo framework suele ir
con la instalación global; el modo biblioteca suele ir con `go get -tool`
(versión fijada en tu `go.mod`). Las tres funcionan para cualquier modo.

```sh
# Option A: mise -- global, easy upgrades
mise use github:andrioid/gastro@latest

# Option B: go install -- global, no extra tooling
go install github.com/andrioid/gastro/cmd/gastro@latest

# Option C: go tool -- per-project version pin in go.mod, no global install
go get -tool github.com/andrioid/gastro/cmd/gastro
```

Con la Opción C, la CLI se invoca como `go tool gastro <cmd>` en lugar de
`gastro <cmd>`, y la versión queda fijada en el `go.mod` de tu proyecto,
conveniente para CI/CD donde prefieras no instalar un binario global.
El andamio (`gastro new`) agrega la directiva `tool` al `go.mod` generado y una directiva `//go:generate go tool gastro generate` a
`main.go`, para que `go tool gastro …` y `go generate ./...` funcionen directamente.

### Explora tu proyecto

```sh
# List all components and pages with their Props signatures
gastro list

# Machine-readable output for scripts and agents
gastro list --json
```

### Formatea tus fuentes

```sh
gastro fmt          # format every .gastro file in the project
gastro fmt --check  # exit non-zero if any file is unformatted (CI)
```

El `gofmt` estándar no puede analizar archivos `.gastro`: el separador de frontmatter `---` no es Go válido. `gastro fmt` es consciente de los separadores: formatea el frontmatter de Go
con `go/format` y ordena/agrupa las importaciones (importaciones estándar de Go primero,
importaciones de componentes después). Las extensiones del editor ejecutan el mismo formateador
al guardar a través del servidor de lenguaje.

### Compilar para producción

```sh
gastro build       # or: go tool gastro build
./app
```

## Cómo funciona

### Formato de archivo

Un archivo `.gastro` tiene dos secciones separadas por `---`:

- **Frontmatter** (entre los delimitadores): código Go que se ejecuta en el servidor.
  Las variables en mayúsculas se exportan a la plantilla. Las variables en minúsculas son
  privadas.
- **Cuerpo de la plantilla** (después del segundo delimitador): sintaxis estándar de `html/template` de Go.

### Visibilidad de variables

Refleja la propia convención de exportación de Go:

```gastro
---
slug := r.PathValue("slug")   // lowercase -> private
err := doSomething()           // lowercase -> private

Title := "Hello"               // Uppercase -> {{ .Title }}
Items := fetchItems()          // Uppercase -> {{ .Items }}
---
<h1>{{ .Title }}</h1>
```

El frontmatter tiene `w http.ResponseWriter` y `r *http.Request` disponibles en el ámbito.
El controlador de la página se ejecuta para cada método HTTP; ramifica según `r.Method` para
manejar solicitudes no GET en el mismo archivo.

### Enrutamiento basado en archivos

| File                       | Route          |
|----------------------------|----------------|
| `pages/index.gastro`       | `/`            |
| `pages/about/index.gastro` | `/about`       |
| `pages/blog/[slug].gastro` | `/blog/{slug}` |

Los patrones son independientes del método; la página maneja cada método (GET, POST,
etc.) y ramifica según `r.Method`.

### Activos estáticos

Los archivos en `static/` se sirven en el prefijo de URL `/static/`. Refiérete a ellos
en las plantillas como `/static/styles.css`, `/static/images/logo.png`, etc.

### Helpers de plantilla integrados

Disponibles en todas las plantillas sin necesidad de registro:

`upper`, `lower`, `trim`, `join`, `split`, `contains`, `replace`,
`default`, `safeHTML`, `safeAttr`, `safeURL`, `safeCSS`, `safeJS`, `dict`,
`list`, `json`, `timeFormat`

Registra helpers personalizados en `main.go`:

```go
router := gastro.New(
    gastro.WithFuncs(template.FuncMap{
        "formatEUR": func(cents int) string {
            return fmt.Sprintf("%.2f EUR", float64(cents)/100)
        },
    }),
)
http.ListenAndServe(":4242", router.Handler())
```

`gastro.New(opts...)` devuelve un `*Router` cuyo `Handler()` montas en
tu servidor HTTP. Otras opciones incluyen `WithDeps[T]` para inyección de dependencias tipada en las páginas, `WithOverride(pattern, handler)` para reemplazar una ruta generada automáticamente con un controlador Go, `WithMiddleware(pattern, fn)` para envolver rutas (comodines de subárbol mediante `"/admin/{path...}"`),
`WithRequestFuncs(binder)` para **helpers de plantilla conscientes de la solicitud** (traductores i18n, tokens CSRF, nonces CSP — consulta [Helpers → Helpers conscientes de la
solicitud](docs/helpers.md#request-aware-helpers-withrequestfuncs)),
`WithClassMerger(fn)` para conectar un unificador de clases compatible con Tailwind detrás de los
helpers `attrs`/`twMerge` (consulta [Componentes → Reenvío de
atributos](docs/components.md#forwarding-attributes)), y
`WithErrorHandler(fn)` para respuestas personalizadas de errores de renderizado (registro, seguimiento de errores, páginas 500 con marca). Consulta [Páginas y Enrutamiento](docs/pages.md) para
la API completa y [Manejo de errores](docs/error-handling.md) para el
catálogo de modos de fallo.

> El método heredado `gastro.Routes(opts...) http.Handler` de uso único se conserva como
> un adaptador obsoleto alrededor de `gastro.New(opts...).Handler()`. Prefiere `New()`
> en código nuevo.

### Despliegue

`gastro build` produce un único binario. Despliega copiándolo al servidor.
Las plantillas y activos estáticos (`static/`) se incrustan en el binario mediante
`//go:embed`, por lo que el binario desplegado es autocontenido: no
`templates/` ni `static/` necesitan enviarse junto a él. En
modo desarrollo (`GASTRO_DEV=1`, establecido automáticamente por `gastro dev`) ambos
se leen desde el disco para que los cambios recarguen en caliente.

```sh
gastro generate
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/myapp .
scp dist/myapp server:/opt/myapp
```

## Soporte para editores

La inteligencia del editor requiere dos binarios en tu PATH:

- **`gastro`** -- incluye el servidor de lenguaje de Gastro (subcomando `gastro lsp`)
- **`gopls`** -- el servidor de lenguaje de Go (para inteligencia de Go en el frontmatter)

```sh
go install github.com/andrioid/gastro/cmd/gastro@latest
go install golang.org/x/tools/gopls@latest
```

Si `gopls` no está en el PATH, `gastro lsp` seguirá proporcionando
completaciones del cuerpo de la plantilla (variables, componentes, funciones), pero la inteligencia de Go del frontmatter
(completaciones, hover, diagnósticos) no estará disponible.

### VS Code

La extensión está en `editors/vscode/`. Para instalarla localmente:

```sh
cd editors/vscode
npm install   # required -- installs vscode-languageclient dependency
# Symlink into VS Code extensions
ln -s "$(pwd)" ~/.vscode/extensions/gastro-vscode
```

Luego recarga VS Code.

### Neovim

Copia o crea un enlace simbólico de `editors/neovim/gastro.lua` a
`~/.config/nvim/after/plugin/gastro.lua`. El LSP se inicia automáticamente para
archivos `.gastro`. Requiere `nvim-treesitter` para resaltado de sintaxis.

### Zed

Una extensión para Zed está en `editors/zed/`. Descarga automáticamente `gastro` desde
los releases de GitHub en el primer uso. Para instalarla como extensión de desarrollo:

1. Abre la paleta de comandos de Zed y ejecuta "zed: install dev extension"
2. Selecciona el directorio `editors/zed/`

## Ejemplo

Consulta `examples/blog/` para un blog completo y funcional con enrutamiento basado en archivos,
páginas dinámicas, helpers de plantilla y activos estáticos.

## Documentación

- [Inicio Rápido — Modo Framework](docs/getting-started.md) -- Proyectos nuevos de gastro
- [Inicio Rápido — Modo Biblioteca](docs/getting-started-library.md) -- Agregar gastro a un proyecto Go existente
- [Páginas](docs/pages.md) -- Guía de autoría de páginas y referencia de API
- [Componentes](docs/components.md) -- Guía de autoría de componentes y referencia de API
- [Markdown](docs/markdown.md) -- Incrustar markdown mediante `//gastro:embed` y renderizar con goldmark
- [Modo Dev](docs/dev-mode.md) -- `gastro dev`, `gastro watch`, variables de entorno, configuraciones compuestas
- [Manejo de errores](docs/error-handling.md) -- Modos de fallo y `WithErrorHandler`
- [Diseño](docs/design.md) -- Documento de diseño completo con todas las decisiones
- [Arquitectura](docs/architecture.md) -- Arquitectura del código y guía de paquetes
- [Contribuir](docs/contributing.md) -- Cómo contribuir

## Licencia

[MIT](LICENSE) © Andri Óskarsson
