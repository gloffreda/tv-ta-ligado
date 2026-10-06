package dataseg

// Modelos dos segmentos de dados (sem LLM). Cada lista tem pelo menos 8
// variações. {s} = saudação ("Boa noite"), {c} = cidade, {r} = região,
// {max}/{min}/{p} = números da previsão, {v} = veículo, {o} = artigo do veículo.
// Falas banter nunca têm números nem nomes fora da allowlist; falas fact
// começam pelo fato (regra "uma fala, um tipo").

// Abertura da sessão: uma fala do Orlando (com a saudação, 24 áudios no
// total, que ficam em cache depois da primeira vez).
var openingLines = []string{
	"{s}. Está começando o TV Tá Ligado.",
	"{s}. Você está no TV Tá Ligado.",
	"{s}. Começa agora o TV Tá Ligado, com tudo checado.",
	"{s}. O TV Tá Ligado está no ar.",
	"{s}. Bem-vindo ao TV Tá Ligado.",
	"{s}. Ligou na hora certa: é o TV Tá Ligado.",
	"{s}. Aqui é o Orlando, e este é o TV Tá Ligado.",
	"{s}. Notícia com fonte, aqui no TV Tá Ligado.",
}

var weatherOpen = []string{
	"{s}, meus amores. Vamos ao tempo, que hoje ele chegou de gala.",
	"{s}! Aqui é a Glória Garoa, com a previsão mais elegante do país.",
	"{s}. O céu se arrumou, e eu também: vamos à previsão.",
	"{s}, Brasil. Hora do tempo, com tapete vermelho e tudo.",
	"{s}! Separei as nuvens por estilo. Vem comigo.",
	"{s}. A previsão de hoje vem com caimento impecável.",
	"{s}, queridos. O tempo pediu passagem, e eu dei.",
	"{s}! Ajeita a gola, que lá vem a previsão do tempo.",
}

var weatherCity = []string{
	"{r}, em {c}, máxima de {max} °C, mínima de {min} °C e {p}% de chance de chuva.",
	"{r}, {c} tem máxima de {max} °C e mínima de {min} °C, com {p}% de chance de chuva.",
	"{r}, a previsão para {c} é de máxima de {max} °C, mínima de {min} °C e {p}% de chance de chuva.",
	"{r}, {c} tem mínima de {min} °C, máxima de {max} °C e chance de chuva de {p}%.",
	"{r}, em {c}, os termômetros vão de {min} °C a {max} °C, e a chance de chuva é de {p}%.",
	"{r}, {c} fica entre {min} °C e {max} °C, com {p}% de chance de chuva.",
	"{r}, olho em {c}: máxima de {max} °C, mínima de {min} °C e {p}% de chance de chuva.",
	"{r}, para {c}, a previsão traz máxima de {max} °C, mínima de {min} °C e {p}% de chance de chuva.",
}

// Artigo + região: "No Sudeste".
var regionLead = map[string]string{"Norte": "No Norte", "Nordeste": "No Nordeste", "Centro-Oeste": "No Centro-Oeste", "Sudeste": "No Sudeste", "Sul": "No Sul"}

var alertIntro = []string{
	"Agora, um aviso oficial. E é sério.",
	"Antes de continuar, um alerta oficial.",
	"Um momento de seriedade: tem aviso oficial.",
	"Atenção agora, que é aviso oficial.",
	"Pausa no glamour: alerta oficial.",
	"Aqui não tem brincadeira: aviso oficial.",
	"Agora, uma informação de segurança.",
	"Fica atento a este aviso oficial.",
}

var weatherCloseRain = "Leva o guarda-chuva. Mas leva com estilo."

var weatherClose = []string{
	"Céu de passarela. Aproveita e capricha no visual.",
	"Por hoje é isso. O tempo passou e deixou perfume.",
	"Essa foi a previsão. Glória Garoa, sempre com estilo.",
	"Fecho a previsão por aqui. Até a próxima, meus amores.",
	"Hidrata, que o tempo também é tendência.",
	"O tempo está dado. O look é com você.",
	"Fim da previsão. A elegância continua.",
	"Por hoje é só. Olha pro céu e me conta depois.",
}

var marketOpen = []string{
	"{s}. Vamos aos números do mercado, com calma e com fonte.",
	"{s}. Hora de olhar os indicadores da economia.",
	"{s}. Os números do dia, um por um.",
	"{s}. Agora, o painel da economia.",
	"{s}. Vamos aos indicadores, sem pressa e sem palpite.",
	"{s}. Separei os números que mexem com o seu bolso.",
	"{s}. A economia em números, como deve ser.",
	"{s}. Agora, os dados oficiais do mercado.",
}

var marketLead = []string{"", "Primeiro: ", "Agora: ", "Na sequência: ", "Também: ", "Outro número: ", "Seguindo: ", "Mais um dado: "}

var marketClose = "Isso não é recomendação de investimento."

var headlinesOpen = map[string][]string{
	"orlando": {
		"{s}. As manchetes desta hora.",
		"{s}. Vamos ao resumo das notícias.",
		"{s}. Estas são as principais notícias do momento.",
		"{s}. O que está acontecendo agora, com fonte.",
		"{s}. As notícias desta hora, uma a uma.",
		"{s}. Vamos às manchetes.",
		"{s}. Um giro pelas notícias.",
		"{s}. Atenção para as manchetes.",
	},
	"duda": {
		"{s}! Bora de manchete?",
		"{s}. Resumão das notícias, tá ligado?",
		"{s}! As manchetes chegaram, quentinhas.",
		"{s}. Vem comigo nas notícias desta hora.",
		"{s}! Notícia boa, notícia séria, tudo com fonte.",
		"{s}. Manchetes na tela, atenção no áudio.",
		"{s}! Pega o café, que lá vêm as manchetes.",
		"{s}. Giro rápido pelas notícias.",
	},
}

// {V}: veículo com artigo maiúsculo ("O g1"); {v}: minúsculo ("o g1").
var headlineLead = []string{
	"{V} informa: ",
	"Segundo {v}: ",
	"De acordo com {v}: ",
	"Notícia d{v}: ",
	"Pel{v}: ",
	"Manchete d{v}: ",
	"Reportagem d{v}: ",
	"{V} publicou: ",
}

var headlinesClose = map[string][]string{
	"orlando": {
		"Essas foram as manchetes desta hora.",
		"E esse foi o resumo das notícias.",
		"Por agora, são essas as manchetes.",
		"Fechamos o giro de notícias.",
		"Essas são as notícias deste momento.",
		"Assim terminam as manchetes.",
		"Esse foi o nosso resumo.",
		"Voltamos com mais notícias em instantes.",
	},
	"duda": {
		"Essas foram as manchetes. Tá ligado?",
		"Resumão entregue. Até daqui a pouco.",
		"Fechou o giro de notícias.",
		"E é isso por agora. Fica ligado.",
		"Manchetes dadas. Já, já tem mais.",
		"Por agora é isso. Tá ligado?",
		"Esse foi o resumão.",
		"Giro de notícias encerrado.",
	},
}

// Artigo de cada veículo (o g1, a Folha).
var outletArticle = map[string]string{
	"g1": "o", "Agência Brasil": "a", "CNN Brasil": "a", "Poder360": "o", "Folha de S.Paulo": "a", "Folha": "a",
	"Estadão": "o", "BBC News Brasil": "a", "BBC": "a", "RFI": "a", "Banco Central": "o", "INMET": "o",
}
